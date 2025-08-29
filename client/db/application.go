package db

import (
	"encoding/hex"
	"fmt"
	"path/filepath"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"

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
		Example: "application-db [list,load-version,info,trace,size]",
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

			method := vp.GetString(FlagAppDBMethod)

			if method == "compact" {
				o := &opt.Options{
					DisableSeeksCompaction: true,
				}
				appDB := filepath.Join(home, "data/application.db")
				db, err := leveldb.OpenFile(appDB, o)
				if err != nil {
					return err
				}
				defer db.Close()
				err = db.CompactRange(util.Range{Start: nil, Limit: nil})
				if err != nil {
					return err
				}
				cmd.Printf("compacted\n")
				return nil
			}

			db, err := openDBApplication(defaultNodeHome, server.GetAppDBBackend(vp))
			if err != nil {
				return err
			}
			defer db.Close()

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
			} else if method == "info" {
				height := vp.GetInt64("height")
				if height == 0 {
					return fmt.Errorf("height is required")
				}

				vp.Set(server.FlagPruning, "nothing")

				logger := log.NewLogger(cmd.OutOrStdout())
				app := appCreator(logger, db, nil, vp)
				cms := app.CommitMultiStore()

				rms, ok := cms.(*rootmulti.Store)
				if ok {
					cInfo, err := rms.GetCommitInfo(height)
					if cInfo != nil && err == nil {
						cmd.Printf("version: %v\n", cInfo.Version)
						cmd.Printf("timestamp: %v\n", cInfo.Timestamp)
						for _, storeInfo := range cInfo.StoreInfos {
							cmd.Printf("store %-30s - hash: %v\n", storeInfo.Name, hex.EncodeToString(storeInfo.CommitId.Hash))
						}
					}
				}
			} else if method == "trace" {
				height := vp.GetInt64("height")
				if height == 0 {
					return fmt.Errorf("height is required")
				}

				traceDB, err := openDBTraceCommit(home, server.GetAppDBBackend(vp))
				if err != nil {
					return err
				}
				defer traceDB.Close()

				exists, err := traceDB.Has([]byte(fmt.Sprintf("trace-%d", height)))
				if err != nil || !exists {
					return fmt.Errorf("trace not found")
				}

				trace, err := traceDB.Get([]byte(fmt.Sprintf("trace-%d", height)))
				if err != nil || trace == nil {
					return err
				}
				cmd.Printf("%v", string(trace))
			} else if method == "size" {
				db.Close()
				appDB := filepath.Join(home, "data/application.db")
				db, err := leveldb.OpenFile(appDB, nil)
				if err != nil {
					return err
				}
				defer db.Close()
				it := db.NewIterator(nil, nil)
				sizes := make(map[string]int64)

				for it.Next() {
					k := it.Key()
					v := it.Value()
					if len(k) >= 20 {
						prefix := string(k[:20])
						sizes[prefix] += int64(len(k) + len(v))
					} else if len(k) > 0 {
						prefix := string(k)
						sizes[prefix] += int64(len(k) + len(v))
					}
				}
				it.Release()

				for p, sz := range sizes {
					// affiche uniquement si supérieur à 10Mi
					if sz > 10*1024*1024 {
						fmt.Printf("Prefix %x: %.2f MiB\n", p, float64(sz)/(1024*1024))
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().Int64("height", 0, "The height to get the info for")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database for application and snapshots databases")

	return cmd
}

func guessStoreNameFromKey(k []byte) string {
	// Lis des ASCII imprimables [32..126] jusqu'à un séparateur probable.
	// Beaucoup de builds utilisent un séparateur '/' ou un 0x00 après le nom du store.
	n := 0
	for n < len(k) {
		b := k[n]
		if b == '/' || b == 0x00 || b < 32 || b > 126 {
			break
		}
		n++
	}
	if n == 0 {
		return "(unknown)"
	}
	return string(k[:n])
}

func openDBApplication(rootDir string, backendType dbm.BackendType) (dbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return dbm.NewDB("application", backendType, dataDir)
}

func openDBTraceCommit(rootDir string, backendType dbm.BackendType) (dbm.DB, error) {
	dataDir := filepath.Join(rootDir, "data")
	return dbm.NewDB("trace", backendType, dataDir)
}

func DeleteLatestApplication(appCreator servertypes.AppCreator, homeDir string, backendType string, cmd *cobra.Command) error {
	vp := viper.New()
	vp.Set(server.FlagPruning, "nothing")

	db, err := openDBApplication(homeDir, dbm.BackendType(backendType))
	if err != nil {
		return err
	}

	logger := log.NewLogger(cmd.OutOrStdout())
	app := appCreator(logger, db, nil, vp)
	cms := app.CommitMultiStore()

	latestVersion := rootmulti.GetLatestVersion(db)
	newLatestVersion := latestVersion - 1

	if newLatestVersion < 0 {
		return fmt.Errorf("version is less than 0")
	}

	err = cms.RollbackToVersion(newLatestVersion)
	if err != nil {
		return err
	}

	cmd.Printf("%v version - loaded\n", newLatestVersion)
	return nil
}
