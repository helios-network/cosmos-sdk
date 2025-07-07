package db

import (
	"bytes"
	"fmt"
	"strconv"

	cometdbm "github.com/cometbft/cometbft-db"
	sm "github.com/cometbft/cometbft/state"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"
)

func ValidateCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [height]",
		Short: "Validate coherence between application, blockstore and state databases",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			if homeDir == "" {
				homeDir = defaultNodeHome
			}

			backendType, _ := cmd.Flags().GetString(FlagAppDBBackend)
			if backendType == "" {
				backendType = "goleveldb"
			}

			targetHeight, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid height: %s", args[0])
			}

			appDB, err := openDBApplication(homeDir, dbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open application db: %w", err)
			}
			defer appDB.Close()

			blockDB, err := openDBBlockStore(homeDir, dbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open blockstore db: %w", err)
			}
			defer blockDB.Close()

			stateDB, err := openDBState(homeDir, cometdbm.BackendType(backendType))
			if err != nil {
				return fmt.Errorf("failed to open state db: %w", err)
			}
			defer stateDB.Close()

			return validateCoherence(cmd, appDB, blockDB, stateDB, targetHeight)
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	return cmd
}

func validateCoherence(cmd *cobra.Command, appDB dbm.DB, blockDB cometdbm.DB, stateDB cometdbm.DB, height int64) error {
	block, err := GetBlock(blockDB, height)
	if err != nil || block == nil {
		return fmt.Errorf("block %d not found in blockstore", height)
	}

	stateStore := sm.NewStore(stateDB, sm.StoreOptions{})
	currentState, err := stateStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	if height > currentState.LastBlockHeight {
		return fmt.Errorf("height %d beyond current state height %d", height, currentState.LastBlockHeight)
	}

	cInfoKey := fmt.Sprintf("s/%d", height)
	cInfoBytes, err := appDB.Get([]byte(cInfoKey))
	if err != nil {
		return fmt.Errorf("failed to check version %d in application db: %w", height, err)
	}
	if cInfoBytes == nil {
		return fmt.Errorf("height %d not found in application database", height)
	}

	if height == currentState.LastBlockHeight {
		if !bytes.Equal(block.Header.AppHash, currentState.AppHash) {
			return fmt.Errorf("AppHash mismatch at height %d", height)
		}
		cmd.Printf("AppHash validation passed at height %d\n", height)
	} else {
		cmd.Printf("Historical height %d validated (AppHash: %X)\n", height, block.Header.AppHash)
	}

	return nil
}
