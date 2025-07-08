package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cmtcmd "github.com/cometbft/cometbft/cmd/cometbft/commands"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server/types"
)

// NewRollbackCmd creates a command to rollback CometBFT and multistore state by one height.
func NewRollbackCmd(appCreator types.AppCreator, defaultNodeHome string) *cobra.Command {
	var removeBlock bool
	var deleteLatestState bool
	var height int64

	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "rollback Cosmos SDK and CometBFT state by one height",
		Long: `
A state rollback is performed to recover from an incorrect application state transition,
when CometBFT has persisted an incorrect app hash and is thus unable to make
progress. Rollback overwrites a state at height n with the state at height n - 1.
The application also rolls back to height n - 1. No blocks are removed, so upon
restarting CometBFT the transactions in block n will be re-executed against the
application.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := GetServerContextFromCmd(cmd)
			cfg := ctx.Config
			home := cfg.RootDir
			db, err := openDB(home, GetAppDBBackend(ctx.Viper))
			if err != nil {
				return err
			}
			app := appCreator(ctx.Logger, db, nil, ctx.Viper)
			// rollback CometBFT state

			latestVersion := app.CommitMultiStore().LatestVersion()
			if latestVersion < height {
				return fmt.Errorf("height %d is greater than latest version %d", height, latestVersion)
			}

			targetHeight := latestVersion - 1
			if targetHeight < 1 {
				return fmt.Errorf("latest version %d is less than 1", latestVersion)
			}

			if height >= 1 {
				targetHeight = height
			}

			hash := []byte{}
			for i := latestVersion; i >= targetHeight; i-- {
				blockStoreHeight, blockStoreHash, err := cmtcmd.RollbackState(ctx.Config, removeBlock)
				if err != nil {
					return fmt.Errorf("failed to rollback CometBFT state: %w", err)
				}
				hash = blockStoreHash
				fmt.Printf("Rolled back block state to height %d and hash %X\n", blockStoreHeight, hash)
				i = blockStoreHeight
				latestVersion = i
			}
			// rollback the multistore

			if err := app.CommitMultiStore().RollbackToVersion(targetHeight); err != nil {
				return fmt.Errorf("failed to rollback to version: %w", err)
			}

			if deleteLatestState {
				newPrivValidatorState := map[string]interface{}{
					"height": "0",
					"round":  0,
					"step":   0,
				}
				privValidatorStatePath := filepath.Join(home, "data/priv_validator_state.json")
				json, err := json.MarshalIndent(newPrivValidatorState, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal priv_validator_state.json: %w", err)
				}
				err = os.WriteFile(privValidatorStatePath, json, 0644)
				if err != nil {
					return fmt.Errorf("failed to write priv_validator_state.json: %w", err)
				}
			}
			fmt.Printf("Rolled back state and application to height %d and hash %X\n", targetHeight, hash)
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().BoolVar(&removeBlock, "hard", false, "remove last block as well as state")
	cmd.Flags().BoolVar(&deleteLatestState, "delete-latest-state", false, "delete latest state")
	cmd.Flags().Int64Var(&height, "height", 0, "height to rollback to")
	return cmd
}
