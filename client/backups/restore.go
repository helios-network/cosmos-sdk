package backups

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// RestoreCmd restores a snapshot
func RestoreCmd(appCreator servertypes.AppCreator, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore [snapshot-file]",
		Short: "Restore a snapshot",
		Long:  "Restore a snapshot from a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get the snapshot file name
			snapshotFile := args[0]

			// Get the server context
			serverCtx := server.GetServerContextFromCmd(cmd)

			// Configure backup settings
			backupEnabled := false
			backupBlockInterval := uint64(1000) // Default interval
			backupDir := "backups"
			minRetainBackups := uint64(999999) // No limit in manual backups

			backupManager := baseapp.NewBackupManager(serverCtx.Logger)
			backupManager.Configure(backupEnabled, backupBlockInterval, backupDir, defaultNodeHome, minRetainBackups, minRetainBackups, nil)

			if err := backupManager.PerformRestore(snapshotFile); err != nil {
				return fmt.Errorf("{ \"error\": \"failed to restore snapshot: %w\" }", err)
			}

			cmd.Printf("{\"message\": \"Snapshot restored successfully\"}")
			return nil
		},
	}

	return cmd
}
