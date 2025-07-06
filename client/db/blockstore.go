package db

import (
	"encoding/hex"
	"fmt"
	"path/filepath"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client/flags"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// BlockInfo represents information about a block in the blockstore
type BlockInfo struct {
	Height int64
	Hash   []byte
}

func openDBBlockStore(rootDir string, backendType dbm.BackendType) (dbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return dbm.NewDB("blockstore", backendType, dataDir)
}

// BlockstoreCmd returns a command to interact with the CometBFT blockstore
func BlockstoreCmd(appCreator servertypes.AppCreator, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blockstore",
		Short: "Interact with CometBFT blockstore",
		Long:  `Commands to interact with the CometBFT blockstore database`,
	}

	cmd.AddCommand(
		listBlocksCmd(defaultNodeHome),
		getBlockCmd(defaultNodeHome),
	)

	return cmd
}

// listBlocksCmd returns a command to list all blocks in the blockstore
func listBlocksCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all blocks in the blockstore",
		Long:  `List all blocks stored in the CometBFT blockstore with their heights and hashes`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb" // default backend
			}

			db, err := openDBBlockStore(homeDir, dbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open blockstore: %w", err)
			}
			defer db.Close()

			blocks, err := listBlocksFromBlockstore(db)
			if err != nil {
				return fmt.Errorf("failed to list blocks: %w", err)
			}

			if len(blocks) == 0 {
				cmd.Printf("No blocks found in blockstore\n")
				return nil
			}

			cmd.Printf("Found %d blocks in blockstore:\n", len(blocks))
			cmd.Printf("%-10s %-64s %s\n", "Height", "Hash", "Size")
			cmd.Printf("%s\n", "--------------------------------------------------------------------------------")

			for _, block := range blocks {
				hashHex := hex.EncodeToString(block.Hash)
				if len(hashHex) > 64 {
					hashHex = hashHex[:64]
				}
				cmd.Printf("%-10d %-64s\n", block.Height, hashHex)
			}

			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")

	return cmd
}

// getBlockCmd returns a command to get a specific block from the blockstore
func getBlockCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [height]",
		Short: "Get a specific block from the blockstore",
		Long:  `Get detailed information about a specific block by height`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb" // default backend
			}

			db, err := openDBBlockStore(homeDir, dbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open blockstore: %w", err)
			}
			defer db.Close()

			// Parse height argument
			var height int64
			if _, err := fmt.Sscanf(args[0], "%d", &height); err != nil {
				return fmt.Errorf("invalid height: %s", args[0])
			}

			block, err := getBlockFromBlockstore(db, height)
			if err != nil {
				return fmt.Errorf("failed to get block %d: %w", height, err)
			}

			if block == nil {
				cmd.Printf("Block %d not found in blockstore\n", height)
				return nil
			}

			cmd.Printf("Block %d:\n", height)
			cmd.Printf("  Hash: %X\n", block.Hash)
			// cmd.Printf("  Size: %d bytes\n", block.Size)
			// cmd.Printf("  Data: %X...\n", block.Data[:min(64, len(block.Data))])

			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")

	return cmd
}

// listBlocksFromBlockstore lists all blocks stored in the CometBFT blockstore
func listBlocksFromBlockstore(db dbm.DB) ([]BlockInfo, error) {
	var blocks []BlockInfo

	iter, err := db.Iterator(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		value := iter.Value()

		// Decode hex key to see the actual format
		keyStr := string(key)

		// Looking at your output, the keys seem to be:
		// - "SC:996", "SC:997", etc. (block heights)
		// - "blockStore" (metadata)

		// Check if this is a block height key (SC: followed by a number)
		if len(keyStr) >= 3 && keyStr[:3] == "SC:" {
			// Extract height from "SC:996" format
			heightStr := keyStr[3:]
			var height int64
			if _, err := fmt.Sscanf(heightStr, "%d", &height); err == nil {
				// Extract hash from the beginning of the value
				var hash []byte
				if len(value) >= 32 {
					hash = value[:32]
				}

				blocks = append(blocks, BlockInfo{
					Height: height,
					Hash:   hash,
				})
			}
		}
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %w", err)
	}

	return blocks, nil
}

// getBlockFromBlockstore gets a specific block by height from the blockstore
func getBlockFromBlockstore(db dbm.DB, height int64) (*BlockInfo, error) {
	// Construct the key for block data: "SC:" + height
	key := fmt.Sprintf("SC:%d", height)

	value, err := db.Get([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("failed to get block data: %w", err)
	}

	if value == nil {
		return nil, nil // Block not found
	}

	// Extract hash from block data
	var hash []byte
	if len(value) >= 32 {
		hash = value[:32]
	}

	return &BlockInfo{
		Height: height,
		Hash:   hash,
	}, nil
}
