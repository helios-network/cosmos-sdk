package db

import (
	"fmt"
	"path/filepath"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"cosmossdk.io/log"
	"cosmossdk.io/store/rootmulti"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

const FlagAppDBBackend = "app-db-backend"
const FlagAppDBMethod = "app-db-method"

// Cmd prunes the sdk root multi store history versions based on the pruning options
// specified by command flags.
func Cmd(appCreator servertypes.AppCreator, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "application-db [method]",
		Short:   "Apply a method to the application database",
		Long:    `Apply a method to the application database`,
		Example: "application-db [list,load-version]",
		Args:    cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// bind flags to the Context's Viper so we can get pruning options.
			vp := viper.New()
			if err := vp.BindPFlags(cmd.Flags()); err != nil {
				return err
			}

			// use the first argument if present to set the pruning method
			if len(args) > 0 {
				vp.Set(FlagAppDBMethod, args[0])
			} else {
				vp.Set(FlagAppDBMethod, "list")
			}

			home := vp.GetString(flags.FlagHome)
			if home == "" {
				home = defaultNodeHome
			}

			db, err := openDBApplication(defaultNodeHome, server.GetAppDBBackend(vp))
			if err != nil {
				return err
			}

			method := vp.GetString(FlagAppDBMethod)
			if method == "list" {

				// logger := log.NewLogger(cmd.OutOrStdout())
				// app := appCreator(logger, db, nil, vp)
				// cms := app.CommitMultiStore()

				// rootMultiStore, ok := cms.(*rootmulti.Store)
				// if !ok {
				// 	return fmt.Errorf("currently only support the pruning of rootmulti.Store type")
				// }
				versions := rootmulti.GetAllVersions(db)
				if len(versions) == 100 {
					cmd.Printf("versions: %v\n", versions[:99])
					cmd.Printf("... and more\n")
				} else {
					cmd.Printf("versions: %v\n", versions)
				}
			} else if method == "load-version" {

				if len(args) < 2 {
					return fmt.Errorf("version is required")
				}

				vp.Set("version", args[1])

				vp.Set(server.FlagPruning, "nothing")

				version := vp.GetInt64("version")

				logger := log.NewLogger(cmd.OutOrStdout())
				app := appCreator(logger, db, nil, vp)
				cms := app.CommitMultiStore()

				latestVersion := rootmulti.GetLatestVersion(db)
				if version > latestVersion {
					return fmt.Errorf("version is greater than the latest version")
				}

				err := cms.RollbackToVersion(version)
				if err != nil {
					return err
				}

				cmd.Printf("%v version - loaded\n", version)
			}

			db.Close()
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database for application and snapshots databases")

	return cmd
}

func openDBApplication(rootDir string, backendType dbm.BackendType) (dbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return dbm.NewDB("application", backendType, dataDir)
}
