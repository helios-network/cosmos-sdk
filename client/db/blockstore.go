package db

import (
	"encoding/hex"
	"fmt"
	"path/filepath"

	cometdbm "github.com/cometbft/cometbft-db"
	tenderminttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client/flags"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"

	bs "github.com/cometbft/cometbft/store"
)

// BlockInfo represents information about a block in the blockstore
type BlockInfo struct {
	Height int64
	Hash   []byte
}

func openDBBlockStore(rootDir string, backendType dbm.BackendType) (cometdbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return cometdbm.NewGoLevelDB("blockstore", dataDir)
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
		rollbackCmd(defaultNodeHome),
		infoCmd(defaultNodeHome),
		deleteLatestBlockCmd(defaultNodeHome),
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

			// Get pagination parameters
			limit, _ := cmd.Flags().GetInt("limit")
			offset, _ := cmd.Flags().GetInt("offset")
			if limit <= 0 {
				limit = 100 // default limit
			}

			blocks, err := listBlocksFromBlockstore(db, limit, offset)
			if err != nil {
				return fmt.Errorf("failed to list blocks: %w", err)
			}

			if len(blocks) == 0 {
				cmd.Printf("No blocks found in blockstore (offset: %d, limit: %d)\n", offset, limit)
				return nil
			}

			cmd.Printf("Showing %d blocks (offset: %d, limit: %d):\n", len(blocks), offset, limit)
			cmd.Printf("%-10s %-64s\n", "Height", "Hash")
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
	cmd.Flags().Int("limit", 100, "Maximum number of blocks to return")
	cmd.Flags().Int("offset", 0, "Number of blocks to skip")

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

// rollbackCmd returns a command to rollback the blockstore to a specific height
func rollbackCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback [height]",
		Short: "Rollback blockstore to a specific height",
		Long:  `Remove all blocks after the specified height from the blockstore. This will delete all blocks with height > specified_height.`,
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

			// Parse height argument
			var targetHeight int64
			if _, err := fmt.Sscanf(args[0], "%d", &targetHeight); err != nil {
				return fmt.Errorf("invalid height: %s", args[0])
			}

			if targetHeight < 0 {
				return fmt.Errorf("height must be non-negative")
			}

			db, err := openDBBlockStore(homeDir, dbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open blockstore: %w", err)
			}
			defer db.Close()

			// First, list all blocks to see what will be deleted
			allBlocks, err := listBlocksFromBlockstore(db, -1, 0)
			if err != nil {
				return fmt.Errorf("failed to list blocks: %w", err)
			}

			var blocksToDelete []BlockInfo
			for _, block := range allBlocks {
				if block.Height > targetHeight {
					blocksToDelete = append(blocksToDelete, block)
				}
			}

			if len(blocksToDelete) == 0 {
				cmd.Printf("No blocks to delete. All blocks are at or below height %d.\n", targetHeight)
				return nil
			}

			cmd.Printf("Found %d blocks to delete (heights > %d):\n", len(blocksToDelete), targetHeight)
			for _, block := range blocksToDelete {
				cmd.Printf("  Height: %d, Hash: %X\n", block.Height, block.Hash)
			}

			// Check if force flag is set
			force, _ := cmd.Flags().GetBool("force")

			if !force {
				// Ask for confirmation
				cmd.Printf("\nWARNING: This will permanently delete %d blocks from the blockstore.\n", len(blocksToDelete))
				cmd.Printf("This operation cannot be undone. Are you sure you want to continue? (y/N): ")

				var response string
				fmt.Scanln(&response)
				if response != "y" && response != "Y" {
					cmd.Println("Rollback cancelled.")
					return nil
				}
			}

			// Perform the rollback
			deletedCount, err := rollbackBlockstore(db, targetHeight)
			if err != nil {
				return fmt.Errorf("failed to rollback blockstore: %w", err)
			}

			cmd.Printf("Successfully deleted %d blocks. Blockstore rolled back to height %d.\n", deletedCount, targetHeight)
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	cmd.Flags().Bool("force", false, "Skip confirmation prompt")

	return cmd
}

// listBlocksFromBlockstore lists blocks stored in the CometBFT blockstore with pagination
func listBlocksFromBlockstore(db cometdbm.DB, limit, offset int) ([]BlockInfo, error) {
	blockStore := bs.NewBlockStore(db)

	// Get current height
	currentHeight := blockStore.Height()
	if currentHeight == 0 {
		return []BlockInfo{}, nil
	}

	// Calculate range to fetch
	startHeight := currentHeight - int64(offset)
	endHeight := startHeight - int64(limit) + 1

	// Ensure we don't go below base height
	baseHeight := blockStore.Base()
	if endHeight < baseHeight {
		endHeight = baseHeight
	}

	// Ensure we don't go below 1
	if endHeight < 1 {
		endHeight = 1
	}

	var blocks []BlockInfo

	// Fetch blocks from highest to lowest height
	for height := startHeight; height >= endHeight; height-- {
		block := blockStore.LoadBlock(height)
		if block != nil {
			blocks = append(blocks, BlockInfo{
				Height: height,
				Hash:   block.Hash(),
			})
		}
	}

	return blocks, nil
}

// getBlockFromBlockstore gets a specific block by height from the blockstore
func getBlockFromBlockstore(db cometdbm.DB, height int64) (*BlockInfo, error) {
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

// rollbackBlockstore removes all blockstore keys with height > targetHeight for all relevant prefixes
func rollbackBlockstore(db cometdbm.DB, targetHeight int64) (int, error) {
	deletedCount := 0
	var maxKeptHeight int64 = -1

	// On fait un seul passage sur toutes les clés
	iter, err := db.Iterator(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		keyStr := string(key)

		// Clés de type <prefix>:<height>
		if len(keyStr) > 2 && keyStr[1] == ':' {
			heightStr := keyStr[2:]
			var height int64
			if _, err := fmt.Sscanf(heightStr, "%d", &height); err == nil {
				if height > targetHeight {
					// Supprimer toutes les clés H:, P:, B:, C:, SC: pour cette hauteur
					for _, p := range []string{"H:", "P:", "B:", "C:", "SC:"} {
						k := []byte(p + fmt.Sprintf("%d", height))
						if err := db.Delete(k); err == nil {
							deletedCount++
						}
					}
				} else {
					if height > maxKeptHeight {
						maxKeptHeight = height
					}
				}
			}
		}
	}

	// Mettre à jour la clé 'height' (dernière hauteur connue)
	if maxKeptHeight >= 0 {
		if err := db.Set([]byte("height"), []byte(fmt.Sprintf("%d", maxKeptHeight))); err != nil {
			return deletedCount, fmt.Errorf("failed to update 'height' key: %w", err)
		}
	}

	return deletedCount, nil
}

// infoCmd returns a command to display blockstore info (height and base)
func infoCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show blockstore info (height and base)",
		Long:  `Display the current height and base of the blockstore`,
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

			blockStore := bs.NewBlockStore(db)

			latestHeight := blockStore.Height()
			base := blockStore.Base()

			cmd.Printf("Blockstore info:\n")
			cmd.Printf("  height: %d\n", latestHeight)
			cmd.Printf("  base:   %d\n", base)

			db.Close()
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")

	return cmd
}

// deleteLatestBlockCmd returns a command to delete the latest block from the blockstore
func deleteLatestBlockCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete-latest-block",
		Short: "Delete the latest block from the blockstore",
		Long:  `Remove the most recent block from the blockstore using blockStore.DeleteLatestBlock()`,
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

			blockStore := bs.NewBlockStore(db)

			// Get current height before deletion
			currentHeight := blockStore.Height()
			if currentHeight == 0 {
				cmd.Printf("Blockstore is empty. No blocks to delete.\n")
				return nil
			}

			// Check if force flag is set
			force, _ := cmd.Flags().GetBool("force")

			if !force {
				// Ask for confirmation
				cmd.Printf("WARNING: This will delete the latest block (height: %d) from the blockstore.\n", currentHeight)
				cmd.Printf("This operation cannot be undone. Are you sure you want to continue? (y/N): ")

				var response string
				fmt.Scanln(&response)
				if response != "y" && response != "Y" {
					cmd.Println("Deletion cancelled.")
					return nil
				}
			}

			// Delete the latest block
			err = blockStore.DeleteLatestBlock()
			if err != nil {
				return fmt.Errorf("failed to delete latest block: %w", err)
			}

			cmd.Printf("Successfully deleted block at height %d.\n", currentHeight)
			cmd.Printf("New latest height: %d\n", blockStore.Height())
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	cmd.Flags().Bool("force", false, "Skip confirmation prompt")

	return cmd
}

func GetBlock(db cometdbm.DB, height int64) (*tenderminttypes.Block, error) {
	blockStore := bs.NewBlockStore(db)
	block := blockStore.LoadBlock(height)
	return block, nil
}
