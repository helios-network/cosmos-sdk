package backups

import (
	"fmt"
	"path/filepath"
	"time"

	"cosmossdk.io/store/rootmulti"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// DumpCmd creates a backup
func DumpCmd(appCreator servertypes.AppCreator, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Create a backup at the current height",
		Long:  "Create a backup of the application state at the current block height",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get the server context
			serverCtx := server.GetServerContextFromCmd(cmd)

			vp := viper.New()
			if err := vp.BindPFlags(cmd.Flags()); err != nil {
				return err
			}

			db, err := openDBApplication(defaultNodeHome, server.GetAppDBBackend(vp))
			if err != nil {
				return err
			}

			// Create the app using the appCreator
			app := appCreator(serverCtx.Logger, db, nil, serverCtx.Viper)
			latestVersion := rootmulti.GetLatestVersion(db)

			// close db
			if err := db.Close(); err != nil {
				return fmt.Errorf("failed to close db: %w", err)
			}

			time.Sleep(5000 * time.Millisecond)

			// Configure backup settings
			backupEnabled := false
			backupBlockInterval := uint64(1000) // Default interval
			backupDir := "backups"
			minRetainBackups := uint64(999999) // No limit in manual backups

			if baseApp, ok := app.(interface {
				SetRootDir(string)
			}); ok {
				baseApp.SetRootDir(defaultNodeHome)
			}

			// Set backup configuration
			if baseApp, ok := app.(interface {
				SetBackupConfig(bool, uint64, string, uint64)
			}); ok {
				baseApp.SetBackupConfig(backupEnabled, backupBlockInterval, backupDir, minRetainBackups)
			}

			// Cast to the specific app type that has PerformBackup
			if baseApp, ok := app.(interface{ PerformBackup(int64) (string, error) }); ok {
				backupPath, err := baseApp.PerformBackup(latestVersion)
				if err != nil {
					return fmt.Errorf("{ \"error\": \"failed to create backup: %w\" }", err)
				}
				cmd.Printf("{\"backupPath\": \"%s\"}", backupPath)
			} else {
				return fmt.Errorf("{ \"error\": \"application does not support PerformBackup\" }")
			}

			return nil
		},
	}

	return cmd
}

func openDBApplication(rootDir string, backendType dbm.BackendType) (dbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return dbm.NewDB("application", backendType, dataDir)
}
