package db

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cometdbm "github.com/cometbft/cometbft-db"
	"github.com/spf13/cobra"

	sm "github.com/cometbft/cometbft/state"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// StateInfo represents information about a state entry
type StateInfo struct {
	Height int64
	Type   string
	Hash   []byte
}

func calcValidatorsKey(height int64) []byte {
	return []byte(fmt.Sprintf("validatorsKey:%v", height))
}

func hasValidatorsKey(db cometdbm.DB, height int64) bool {
	key := calcValidatorsKey(height)
	value, err := db.Get(key)
	if err != nil {
		return false
	}
	fmt.Println("value", value)
	return value != nil
}

// StatedbCmd returns a command to interact with the CometBFT state database
func StatedbCmd(appCreator servertypes.AppCreator, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "statedb",
		Short: "Interact with CometBFT state database",
		Long:  `Commands to interact with the CometBFT state database`,
	}

	cmd.AddCommand(
		listStateEntriesCmd(defaultNodeHome),
		getStateCmd(defaultNodeHome),
		rollbackStatedbCmd(defaultNodeHome),
		infoStatedbCmd(defaultNodeHome),
		deleteLatestStateCmd(defaultNodeHome),
	)

	return cmd
}

// listStateEntriesCmd returns a command to list state entries with pagination
func listStateEntriesCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List state entries with pagination",
		Long:  `List state entries stored in the CometBFT state database with pagination support`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb" // default backend
			}

			db, err := openDBState(homeDir, cometdbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open state database: %w", err)
			}
			defer db.Close()

			// Get pagination parameters
			limit, _ := cmd.Flags().GetInt("limit")
			offset, _ := cmd.Flags().GetInt("offset")
			if limit <= 0 {
				limit = 100 // default limit
			}

			entries, err := listStateEntriesFromDB(db, limit, offset)
			if err != nil {
				return fmt.Errorf("failed to list state entries: %w", err)
			}

			if len(entries) == 0 {
				cmd.Printf("No state entries found in statedb (offset: %d, limit: %d)\n", offset, limit)
				return nil
			}

			cmd.Printf("Showing %d state entries (offset: %d, limit: %d):\n", len(entries), offset, limit)
			cmd.Printf("%-10s %-20s %-64s\n", "Height", "Type", "Hash")
			cmd.Printf("%s\n", "--------------------------------------------------------------------------------")

			for _, entry := range entries {
				hashHex := hex.EncodeToString(entry.Hash)
				if len(hashHex) > 64 {
					hashHex = hashHex[:64]
				}
				cmd.Printf("%-10d %-20s %-64s\n", entry.Height, entry.Type, hashHex)
			}

			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	cmd.Flags().Int("limit", 100, "Maximum number of entries to return")
	cmd.Flags().Int("offset", 0, "Number of entries to skip")

	return cmd
}

// getStateCmd returns a command to get the main state
func getStateCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get the main state",
		Long:  `Get detailed information about the main state`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb" // default backend
			}

			db, err := openDBState(homeDir, cometdbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open state database: %w", err)
			}
			defer db.Close()

			stateStore := sm.NewStore(db, sm.StoreOptions{})
			state, err := stateStore.Load()
			if err != nil {
				return fmt.Errorf("failed to load main state: %w", err)
			}

			if state.LastBlockHeight == 0 {
				cmd.Printf("Main state not found in statedb\n")
				return nil
			}

			cmd.Printf("Main State:\n")
			cmd.Printf("  LastBlockHeight: %d\n", state.LastBlockHeight)
			cmd.Printf("  ChainID: %s\n", state.ChainID)
			cmd.Printf("  AppHash: %X\n", state.AppHash)

			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")

	return cmd
}

// rollbackStatedbCmd returns a command to rollback the statedb to a specific height
func rollbackStatedbCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback [height]",
		Short: "Rollback statedb to a specific height",
		Long:  `Remove all state entries after the specified height from the statedb. This will delete all entries with height > specified_height.`,
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

			db, err := openDBState(homeDir, cometdbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open state database: %w", err)
			}
			defer db.Close()

			// Check if force flag is set
			force, _ := cmd.Flags().GetBool("force")

			if !force {
				// Ask for confirmation
				cmd.Printf("WARNING: This will permanently delete all state entries with height > %d from the statedb.\n", targetHeight)
				cmd.Printf("This operation cannot be undone. Are you sure you want to continue? (y/N): ")

				var response string
				fmt.Scanln(&response)
				if response != "y" && response != "Y" {
					cmd.Println("Rollback cancelled.")
					return nil
				}
			}

			// Perform the rollback using the official API
			stateStore := sm.NewStore(db, sm.StoreOptions{})

			protoState, err := stateStore.Load()
			if err != nil {
				return fmt.Errorf("failed to load main state: %w", err)
			}

			err = stateStore.PruneStates(targetHeight+1, protoState.LastBlockHeight, 0) // Prune from targetHeight+1 to MaxInt64, batch size 0 (default)
			if err != nil {
				return fmt.Errorf("failed to rollback statedb: %w", err)
			}

			cmd.Printf("Successfully rolled back statedb to height %d.\n", targetHeight)
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	cmd.Flags().Bool("force", false, "Skip confirmation prompt")

	return cmd
}

// infoStatedbCmd returns a command to display statedb info
func infoStatedbCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show statedb info (latest height)",
		Long:  `Display the current latest height of the statedb`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb" // default backend
			}

			db, err := openDBState(homeDir, cometdbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open state database: %w", err)
			}
			defer db.Close()

			it, err := db.Iterator(nil, nil)
			if err != nil {
				return fmt.Errorf("failed to create iterator: %w", err)
			}
			defer it.Close()

			for ; it.Valid(); it.Next() {
				if strings.Contains(string(it.Key()), "validatorsKey") {
					continue
				}
				if strings.Contains(string(it.Key()), "consensusParamsKey") {
					continue
				}
				if strings.Contains(string(it.Key()), "abciResponsesKey") {
					continue
				}
				fmt.Printf("Key: %s\n", string(it.Key()))
				// fmt.Printf("Value: %s\n", it.Value())
			}

			stateStore := sm.NewStore(db, sm.StoreOptions{})
			state, err := stateStore.Load()

			if err != nil {
				cmd.Printf("Statedb is empty or corrupted.\n")
				cmd.Printf("error: %s\n", err.Error())
				return nil
			}

			protoState, err := state.ToProto()
			if err != nil {
				return fmt.Errorf("failed to convert main state to proto: %w", err)
			}
			fmt.Println("protoState.Version", protoState.Version)
			fmt.Println("protoState.ChainID", protoState.ChainID)
			fmt.Println("protoState.InitialHeight", protoState.InitialHeight)
			fmt.Println("protoState.LastBlockHeight", protoState.LastBlockHeight)
			fmt.Println("protoState.LastBlockID.Hash", hex.EncodeToString(protoState.LastBlockID.Hash))
			fmt.Println("protoState.LastBlockID.PartSetHeader.Hash", hex.EncodeToString(protoState.LastBlockID.PartSetHeader.Hash))
			fmt.Println("protoState.LastBlockID.PartSetHeader.Total", protoState.LastBlockID.PartSetHeader.Total)
			fmt.Println("protoState.LastBlockTime", protoState.LastBlockTime)
			fmt.Println("protoState.NextValidators", protoState.NextValidators)
			fmt.Println("protoState.Validators", protoState.Validators.String())
			fmt.Println("protoState.LastValidators", protoState.LastValidators.String())
			fmt.Println("protoState.LastHeightValidatorsChanged", protoState.LastHeightValidatorsChanged)
			fmt.Println("protoState.ConsensusParams", protoState.ConsensusParams)
			fmt.Println("protoState.LastHeightConsensusParamsChanged", protoState.LastHeightConsensusParamsChanged)
			fmt.Println("protoState.LastResultsHash", hex.EncodeToString(protoState.LastResultsHash))
			fmt.Println("protoState.AppHash", hex.EncodeToString(protoState.AppHash))
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")

	return cmd
}

// deleteLatestStateCmd returns a command to delete the latest state
func deleteLatestStateCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete-latest",
		Short: "Delete the latest state from the statedb",
		Long:  `Remove the most recent state from the statedb`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb" // default backend
			}

			err := DeleteLatestState(homeDir, backendType, cmd)
			if err != nil {
				return fmt.Errorf("failed to delete latest state: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	cmd.Flags().Bool("force", false, "Skip confirmation prompt")

	return cmd
}

// openDBState opens the CometBFT state database
func openDBState(rootDir string, backendType cometdbm.BackendType) (cometdbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return cometdbm.NewDB("state", backendType, dataDir)
}

// listStateEntriesFromDB lists state entries with pagination
func listStateEntriesFromDB(db cometdbm.DB, limit, offset int) ([]StateInfo, error) {
	var allEntries []StateInfo

	iter, err := db.Iterator(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		value := iter.Value()

		keyStr := string(key)

		// Parse different key types
		if keyStr == "s" {
			// Main state - for now just show the key exists
			allEntries = append(allEntries, StateInfo{
				Height: 0, // We'll get this from the state later
				Type:   "MainState",
				Hash:   value, // Use the raw bytes as hash for now
			})
		} else if len(keyStr) > 10 && keyStr[:10] == "Validators" {
			// Validators:<height>
			heightStr := keyStr[11:] // Skip "Validators:"
			var height int64
			if _, err := fmt.Sscanf(heightStr, "%d", &height); err == nil {
				allEntries = append(allEntries, StateInfo{
					Height: height,
					Type:   "Validators",
					Hash:   value, // Use value as hash for now
				})
			}
		} else if len(keyStr) > 16 && keyStr[:16] == "ConsensusParams" {
			// ConsensusParams:<height>
			heightStr := keyStr[17:] // Skip "ConsensusParams:"
			var height int64
			if _, err := fmt.Sscanf(heightStr, "%d", &height); err == nil {
				allEntries = append(allEntries, StateInfo{
					Height: height,
					Type:   "ConsensusParams",
					Hash:   value, // Use value as hash for now
				})
			}
		} else if len(keyStr) > 12 && keyStr[:12] == "ABCIResponses" {
			// ABCIResponses:<height>
			heightStr := keyStr[13:] // Skip "ABCIResponses:"
			var height int64
			if _, err := fmt.Sscanf(heightStr, "%d", &height); err == nil {
				allEntries = append(allEntries, StateInfo{
					Height: height,
					Type:   "ABCIResponses",
					Hash:   value, // Use value as hash for now
				})
			}
		}
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %w", err)
	}

	// Sort entries by height (descending - latest first)
	for i := 0; i < len(allEntries); i++ {
		for j := i + 1; j < len(allEntries); j++ {
			if allEntries[i].Height < allEntries[j].Height {
				allEntries[i], allEntries[j] = allEntries[j], allEntries[i]
			}
		}
	}

	// Apply pagination
	start := offset
	end := offset + limit
	if start >= len(allEntries) {
		return []StateInfo{}, nil // No entries in this range
	}
	if end > len(allEntries) {
		end = len(allEntries)
	}

	return allEntries[start:end], nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func DeleteLatestState(homeDir string, backendType string, cmd *cobra.Command) error {
	// Get the latest state to see what will be deleted
	db, err := openDBState(homeDir, cometdbm.BackendType(backendType))
	if err != nil {
		return fmt.Errorf("failed to open state database: %w", err)
	}
	stateStore := sm.NewStore(db, sm.StoreOptions{})
	latestState, err := stateStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load latest state: %w", err)
	}
	currentHeight := latestState.LastBlockHeight
	newLatestHeight := currentHeight - 1
	db.Close()
	// ------------------------------------------------------------

	blockstoreDB, err := openDBBlockStore(homeDir, dbm.BackendType(backendType))
	if err != nil {
		return fmt.Errorf("failed to open blockstore database: %w", err)
	}

	lastBlock, err := GetBlock(blockstoreDB, newLatestHeight)
	if err != nil {
		return fmt.Errorf("failed to load block: %w", err)
	}
	lastBlockProto, err := lastBlock.ToProto()
	if err != nil {
		return fmt.Errorf("failed to convert block to proto: %w", err)
	}
	currentBlock, err := GetBlock(blockstoreDB, currentHeight)
	if err != nil {
		return fmt.Errorf("failed to load block: %w", err)
	}
	// currentBlockProto, err := currentBlock.ToProto()
	// if err != nil {
	// 	return fmt.Errorf("failed to convert block to proto: %w", err)
	// }

	db, err = openDBState(homeDir, cometdbm.BackendType(backendType))
	if err != nil {
		return fmt.Errorf("failed to open state database: %w", err)
	}
	defer db.Close()

	// Get the latest state to see what will be deleted
	stateStore = sm.NewStore(db, sm.StoreOptions{})
	latestState, err = stateStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load latest state: %w", err)
	}

	if latestState.LastBlockHeight == 0 {
		cmd.Println("No state found in the database.")
		return nil
	}
	currentHeight = latestState.LastBlockHeight

	if !hasValidatorsKey(db, currentHeight) {
		cmd.Printf("Validators key not found at height %d. Deletion cancelled.\n", currentHeight)
		return nil
	}

	// Check if force flag is set
	force, _ := cmd.Flags().GetBool("force")

	if !force {
		// Ask for confirmation
		cmd.Printf("WARNING: This will delete the latest state (height: %d) from the statedb.\n", currentHeight)
		cmd.Printf("This operation cannot be undone. Are you sure you want to continue? (y/N): ")

		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			cmd.Println("Deletion cancelled.")
			return nil
		}
	}

	validators, err := stateStore.LoadValidators(newLatestHeight)
	if err != nil {
		return fmt.Errorf("failed to load validators: %w", err)
	}

	lastFinalizeBlockResponse, err := stateStore.LoadFinalizeBlockResponse(newLatestHeight)
	if err != nil {
		return fmt.Errorf("failed to load finalize block response: %w", err)
	}

	// Delete the latest state using the official API
	err = stateStore.PruneStates(newLatestHeight, currentHeight, currentHeight) // Prune only the current height
	if err != nil {
		return fmt.Errorf("failed to delete latest state: %w", err)
	}

	err = stateStore.ReplaceLastFinalizeBlockResponse(newLatestHeight, lastFinalizeBlockResponse)
	if err != nil {
		return fmt.Errorf("failed to replace last finalize block response: %w", err)
	}

	latestState.AppHash = lastFinalizeBlockResponse.AppHash
	latestState.LastBlockHeight = newLatestHeight
	latestState.LastBlockID = currentBlock.LastBlockID
	latestState.LastBlockTime = lastBlockProto.Header.Time
	latestState.Validators = validators
	latestState.LastValidators = validators
	latestState.NextValidators = validators
	latestState.LastHeightValidatorsChanged = newLatestHeight - 1
	latestState.ConsensusParams = latestState.ConsensusParams.Update(lastFinalizeBlockResponse.ConsensusParamUpdates)
	latestState.LastHeightConsensusParamsChanged = newLatestHeight + 1
	latestState.LastResultsHash = lastBlockProto.Header.LastResultsHash
	latestState.AppHash = lastFinalizeBlockResponse.AppHash

	err = stateStore.Save(latestState)
	if err != nil {
		return fmt.Errorf("failed to save latest state: %w", err)
	}

	// remise à zero du fichier priv_validator_state.json
	newPrivValidatorState := map[string]interface{}{
		"height": "0",
		"round":  0,
		"step":   0,
	}
	privValidatorStatePath := filepath.Join(homeDir, "priv_validator_state.json")
	json, err := json.MarshalIndent(newPrivValidatorState, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal priv_validator_state.json: %w", err)
	}
	err = os.WriteFile(privValidatorStatePath, json, 0644)
	if err != nil {
		return fmt.Errorf("failed to write priv_validator_state.json: %w", err)
	}
	///////////////////////////////////////////////////////////

	cmd.Printf("Successfully deleted state at height %d.\n", currentHeight)
	return nil
}
