package db

import (
	"encoding/json"
	"fmt"
	"os"

	cometdbm "github.com/cometbft/cometbft-db"
	sm "github.com/cometbft/cometbft/state"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"
)

// Version simplifiée pour l'import qui évite les problèmes de désérialisation
type ImportStateSnapshot struct {
	Height          int64                      `json:"height"`
	Block           json.RawMessage            `json:"block"`
	ConsensusState  json.RawMessage            `json:"consensus_state"`
	AppState        map[string]json.RawMessage `json:"app_state"`
	AppVersion      int64                      `json:"app_version"`
	ValidatorSet    json.RawMessage            `json:"validator_set"`
	ConsensusParams json.RawMessage            `json:"consensus_params"`
	ExportTime      string                     `json:"export_time"`
	BlockHash       string                     `json:"block_hash"`
	AppHash         string                     `json:"app_hash"`
	ChainID         string                     `json:"chain_id"`
}

func ImportStateCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import-state [snapshot-file]",
		Short: "Import and restore complete state from snapshot",
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

			snapshot, err := loadImportSnapshot(args[0])
			if err != nil {
				return fmt.Errorf("failed to load snapshot: %w", err)
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

			return restoreImportState(cmd, appDB, blockDB, stateDB, snapshot)
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().String(FlagAppDBBackend, "", "The type of database backend")
	cmd.Flags().Bool("force", false, "Skip confirmation prompt")
	return cmd
}

func loadImportSnapshot(filename string) (*ImportStateSnapshot, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var snapshot ImportStateSnapshot
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, err
	}

	return &snapshot, nil
}

func restoreImportState(cmd *cobra.Command, appDB dbm.DB, blockDB cometdbm.DB, stateDB cometdbm.DB, snapshot *ImportStateSnapshot) error {
	// Vérifier état actuel
	stateStore := sm.NewStore(stateDB, sm.StoreOptions{})
	currentState, err := stateStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load current state: %w", err)
	}

	currentHeight := currentState.LastBlockHeight
	targetHeight := snapshot.Height

	if targetHeight >= currentHeight {
		return fmt.Errorf("snapshot height %d >= current height %d, nothing to rollback",
			targetHeight, currentHeight)
	}

	// Avertissement
	cmd.Printf("⚠️  WARNING: Rollback from height %d to %d\n", currentHeight, targetHeight)
	cmd.Printf("⚠️  This will DELETE blocks %d to %d PERMANENTLY\n", targetHeight+1, currentHeight)

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		cmd.Printf("Continue? (y/N): ")
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			return fmt.Errorf("import cancelled")
		}
	}

	// Rollback des 3 bases
	cmd.Printf("Rolling back to height %d...\n", targetHeight)

	// 1. Rollback blockstore
	if _, err := rollbackBlockstore(blockDB, targetHeight); err != nil {
		return fmt.Errorf("failed to rollback blockstore: %w", err)
	}

	// 2. Rollback state
	if err := stateStore.PruneStates(targetHeight+1, currentHeight, 0); err != nil {
		return fmt.Errorf("failed to rollback state: %w", err)
	}

	// 3. Restaurer application state
	if err := restoreApplicationState(appDB, snapshot); err != nil {
		return fmt.Errorf("failed to restore application state: %w", err)
	}

	// Validation finale
	if err := validateCoherence(cmd, appDB, blockDB, stateDB, targetHeight); err != nil {
		return fmt.Errorf("validation failed after restore: %w", err)
	}

	cmd.Printf("State successfully restored to height %d\n", targetHeight)
	cmd.Printf("Chain: %s, AppHash: %s\n", snapshot.ChainID, snapshot.AppHash)

	return nil
}

func restoreApplicationState(appDB dbm.DB, snapshot *ImportStateSnapshot) error {
	// Extraire les données application du snapshot
	appStateData, exists := snapshot.AppState["application"]
	if !exists {
		return fmt.Errorf("no application state found in snapshot")
	}

	var exportData struct {
		Version  int64             `json:"version"`
		Height   int64             `json:"height"`
		KeyCount int               `json:"key_count"`
		RawData  map[string]string `json:"raw_data"`
	}

	if err := json.Unmarshal(appStateData, &exportData); err != nil {
		return fmt.Errorf("failed to unmarshal application state: %w", err)
	}

	// Vider la base application
	if err := clearApplicationDB(appDB); err != nil {
		return fmt.Errorf("failed to clear application db: %w", err)
	}

	// Restaurer toutes les clés
	for keyHex, valueHex := range exportData.RawData {
		key, err := hexDecode(keyHex)
		if err != nil {
			return fmt.Errorf("failed to decode key %s: %w", keyHex, err)
		}

		value, err := hexDecode(valueHex)
		if err != nil {
			return fmt.Errorf("failed to decode value for key %s: %w", keyHex, err)
		}

		if err := appDB.Set(key, value); err != nil {
			return fmt.Errorf("failed to set key %s: %w", keyHex, err)
		}
	}

	return nil
}

func clearApplicationDB(appDB dbm.DB) error {
	iter, err := appDB.Iterator(nil, nil)
	if err != nil {
		return err
	}
	defer iter.Close()

	var keysToDelete [][]byte
	for ; iter.Valid(); iter.Next() {
		keysToDelete = append(keysToDelete, iter.Key())
	}

	for _, key := range keysToDelete {
		if err := appDB.Delete(key); err != nil {
			return err
		}
	}

	return nil
}

func hexDecode(hexStr string) ([]byte, error) {
	if len(hexStr)%2 != 0 {
		return nil, fmt.Errorf("invalid hex string length")
	}

	result := make([]byte, len(hexStr)/2)
	for i := 0; i < len(hexStr); i += 2 {
		var b byte
		_, err := fmt.Sscanf(hexStr[i:i+2], "%02X", &b)
		if err != nil {
			return nil, err
		}
		result[i/2] = b
	}
	return result, nil
}
