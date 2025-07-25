package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/version"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
)

// const (
// 	FlagHeight           = "height"
// 	FlagForZeroHeight    = "for-zero-height"
// 	FlagJailAllowedAddrs = "jail-allowed-addrs"
// 	FlagModulesToExport  = "modules-to-export"
// )

// Genesis types for processing
type Genesis struct {
	AppState      map[string]json.RawMessage `json:"app_state"`
	InitialHeight int64                      `json:"initial_height"`
	Consensus     json.RawMessage            `json:"consensus,omitempty"`
}

type BankBalance struct {
	Address string `json:"address"`
	Coins   []Coin `json:"coins"`
}

type Coin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

type StakingState struct {
	DelegationBoosts []DelegationBoost `json:"delegation_boosts"`
	Delegations      []Delegation      `json:"delegations"`
	Validators       []interface{}     `json:"validators"`
}

type DelegationBoost struct {
	DelegatorAddress string `json:"delegator_address"`
	Amount           string `json:"amount"`
}

type Delegation struct {
	DelegatorAddress string        `json:"delegator_address"`
	AssetWeights     []AssetWeight `json:"asset_weights"`
}

type AssetWeight struct {
	Denom      string `json:"denom"`
	BaseAmount string `json:"base_amount"`
}

type AuthAccount struct {
	Type        string      `json:"@type"`
	BaseAccount BaseAccount `json:"base_account"`
	Permissions []string    `json:"permissions,omitempty"`
	CodeHash    string      `json:"code_hash,omitempty"`
	Name        string      `json:"name,omitempty"`
}

type PubKey struct {
	Type  string `json:"@type"`
	Value string `json:"value"`
}

type BaseAccount struct {
	Address       string  `json:"address"`
	PubKey        *PubKey `json:"pub_key,omitempty"`
	AccountNumber string  `json:"account_number"`
	Sequence      string  `json:"sequence"`
}

type HyperionSubState struct {
	Batches            []interface{} `json:"batches"`
	UnbatchedTransfers []interface{} `json:"unbatched_transfers"`
	BatchConfirms      []interface{} `json:"batch_confirms"`
}

type HyperionState struct {
	SubStates []HyperionSubState `json:"sub_states"`
}

// Helper function to add two string amounts
func addStringAmounts(a, b string) string {
	bigA, _ := new(big.Int).SetString(a, 10)
	bigB, _ := new(big.Int).SetString(b, 10)
	result := new(big.Int).Add(bigA, bigB)
	return result.String()
}

// Helper function to check if amount is greater than threshold
func isAmountGreaterThan(amount, threshold string) bool {
	bigAmount, _ := new(big.Int).SetString(amount, 10)
	bigThreshold, _ := new(big.Int).SetString(threshold, 10)
	return bigAmount.Cmp(bigThreshold) > 0
}

// Helper function to find balance by address
func findBalance(balances []BankBalance, address string) *BankBalance {
	for i := range balances {
		if balances[i].Address == address {
			return &balances[i]
		}
	}
	return nil
}

// Helper function to find coin by denom
func findCoin(coins []Coin, denom string) *Coin {
	for i := range coins {
		if coins[i].Denom == denom {
			return &coins[i]
		}
	}
	return nil
}

// Process genesis for soft reset
func processGenesisSoftReset(genesisJSON *Genesis, tinyGenesis []byte, walletAddressOfInitializer string) (*Genesis, error) {
	fmt.Println("Adding boost balances back to the delegator")

	// Parse staking state
	var stakingState map[string]interface{}
	if err := json.Unmarshal(genesisJSON.AppState["staking"], &stakingState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal staking state: %w", err)
	}

	var bankState map[string]interface{}
	if err := json.Unmarshal(genesisJSON.AppState["bank"], &bankState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bank state: %w", err)
	}

	// Add boost balances back to the delegator
	for _, delegationBoost := range stakingState["delegation_boosts"].([]interface{}) {
		balanceFound := false
		for _, balance := range bankState["balances"].([]interface{}) {
			if balance.(map[string]interface{})["address"] == delegationBoost.(map[string]interface{})["delegator_address"].(string) {
				balanceFound = true
				for _, coin := range balance.(map[string]interface{})["coins"].([]interface{}) {
					if coin.(map[string]interface{})["denom"] == "ahelios" {
						coin.(map[string]interface{})["amount"] = addStringAmounts(coin.(map[string]interface{})["amount"].(string), delegationBoost.(map[string]interface{})["amount"].(string))
					}
				}
			}
		}

		if !balanceFound {
			bankState["balances"] = append(bankState["balances"].([]interface{}), map[string]interface{}{
				"address": delegationBoost.(map[string]interface{})["delegator_address"].(string),
				"coins": []interface{}{
					map[string]interface{}{
						"denom":  "ahelios",
						"amount": delegationBoost.(map[string]interface{})["amount"].(string),
					},
				},
			})
		}
	}

	// Remove delegation boosts
	stakingState["delegation_boosts"] = []interface{}{}

	fmt.Println("Adding delegation balances back to the delegator")
	// Add the delegation to the balance of the delegator
	for _, delegation := range stakingState["delegations"].([]interface{}) {
		balanceFound := false
		for _, balance := range bankState["balances"].([]interface{}) {
			if balance.(map[string]interface{})["address"] == delegation.(map[string]interface{})["delegator_address"].(string) {
				balanceFound = true
				for _, assetWeight := range delegation.(map[string]interface{})["asset_weights"].([]interface{}) {
					foundCoin := false
					for _, coin := range balance.(map[string]interface{})["coins"].([]interface{}) {
						if coin.(map[string]interface{})["denom"] == assetWeight.(map[string]interface{})["denom"].(string) {
							foundCoin = true
							coin.(map[string]interface{})["amount"] = addStringAmounts(coin.(map[string]interface{})["amount"].(string), assetWeight.(map[string]interface{})["base_amount"].(string))
						}
					}
					if !foundCoin {
						balance.(map[string]interface{})["coins"] = append(balance.(map[string]interface{})["coins"].([]interface{}), map[string]interface{}{
							"denom":  assetWeight.(map[string]interface{})["denom"].(string),
							"amount": assetWeight.(map[string]interface{})["base_amount"].(string),
						})
					}
				}
			}
		}

		if !balanceFound {
			coins := make([]interface{}, len(delegation.(map[string]interface{})["asset_weights"].([]interface{})))
			for i, assetWeight := range delegation.(map[string]interface{})["asset_weights"].([]interface{}) {
				coins[i] = assetWeight
			}
			bankState["balances"] = append(bankState["balances"].([]interface{}), map[string]interface{}{
				"address": delegation.(map[string]interface{})["delegator_address"].(string),
				"coins":   coins,
			})
		}
	}

	stakingState["validators"] = []interface{}{}
	stakingState["delegations"] = []interface{}{}

	fmt.Println("Removing distribution and staking pool balances")
	// Remove specific module balances
	for i := range bankState["balances"].([]interface{}) {
		if bankState["balances"].([]interface{})[i].(map[string]interface{})["address"] == "helios1jv65s3grqf6v6jl3dp4t6c9t9rk99cd8nte205" { // distribution module
			bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"] = []interface{}{}
		}
		if bankState["balances"].([]interface{})[i].(map[string]interface{})["address"] == "helios1fl48vsnmsdzcv85q5d2q4z5ajdha8yu3p05elu" { // staking pool
			bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"] = []interface{}{}
		}
		if bankState["balances"].([]interface{})[i].(map[string]interface{})["address"] == "helios13c59hc2zmcrzzxfgh0umpf077cz86pytvzxda6" { // boosted pool
			bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"] = []interface{}{}
		}
	}

	fmt.Println("Removing hyperion sub states (batches, unbatched_transfers, batch_confirms)")
	// Parse hyperion state
	var hyperionState map[string]interface{}
	if err := json.Unmarshal(genesisJSON.AppState["hyperion"], &hyperionState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hyperion state: %w", err)
	}

	for i := range hyperionState["sub_states"].([]interface{}) {
		if len(hyperionState["sub_states"].([]interface{})[i].(map[string]interface{})["batches"].([]interface{})) > 0 {
			// TODO: Manage send back to the sender
			hyperionState["sub_states"].([]interface{})[i].(map[string]interface{})["batches"] = []interface{}{}
		}
		if len(hyperionState["sub_states"].([]interface{})[i].(map[string]interface{})["unbatched_transfers"].([]interface{})) > 0 {
			hyperionState["sub_states"].([]interface{})[i].(map[string]interface{})["unbatched_transfers"] = []interface{}{}
			// TODO: Manage send back to the sender
		}
		if len(hyperionState["sub_states"].([]interface{})[i].(map[string]interface{})["batch_confirms"].([]interface{})) > 0 {
			hyperionState["sub_states"].([]interface{})[i].(map[string]interface{})["batch_confirms"] = []interface{}{}
			// TODO: Manage send back to the sender
		}
	}

	// Clear consensus validators
	genesisJSON.Consensus = nil

	fmt.Println("Sorting balances by denom and removing 0 balances")
	// Sort by denom and remove 0 balances
	for i := range bankState["balances"].([]interface{}) {
		// Filter out 0 balances
		var filteredCoins []interface{}
		for _, coin := range bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"].([]interface{}) {
			if isAmountGreaterThan(coin.(map[string]interface{})["amount"].(string), "0") {
				filteredCoins = append(filteredCoins, coin)
			}
		}
		bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"] = filteredCoins

		// Sort by denom
		sort.Slice(bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"].([]interface{}), func(j, k int) bool {
			return bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"].([]interface{})[j].(map[string]interface{})["denom"].(string) < bankState["balances"].([]interface{})[i].(map[string]interface{})["coins"].([]interface{})[k].(map[string]interface{})["denom"].(string)
		})
	}

	// Remove balances with little ahelios and no other coins
	// var filteredBalances []interface{}
	// for _, balance := range bankState["balances"].([]interface{}) {
	// 	aheliosBalance := nil
	// 	for _, coin := range balance.(map[string]interface{})["coins"].([]interface{}) {
	// 		if coin.(map[string]interface{})["denom"] == "ahelios" {
	// 			aheliosBalance = coin
	// 			break
	// 		}
	// 	}
	// 	if aheliosBalance != nil {
	// 		if len(balance.(map[string]interface{})["coins"].([]interface{})) > 1 {
	// 			filteredBalances = append(filteredBalances, balance)
	// 		} else if isAmountGreaterThan(aheliosBalance.Amount, "1000000000000000000") {
	// 			filteredBalances = append(filteredBalances, balance)
	// 		}
	// 	} else {
	// 		filteredBalances = append(filteredBalances, balance)
	// 	}
	// }
	// bankBalances = filteredBalances

	fmt.Println("Merging genesis with tiny genesis")
	// Parse tiny genesis
	var tinyGenesisJSON Genesis
	if err := json.Unmarshal(tinyGenesis, &tinyGenesisJSON); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tiny genesis: %w", err)
	}

	tinyGenesisJSON.InitialHeight = genesisJSON.InitialHeight

	// Update ERC20 state
	tinyGenesisJSON.AppState["erc20"] = genesisJSON.AppState["erc20"]

	// Update auth accounts
	var authAccounts []AuthAccount
	if err := json.Unmarshal(genesisJSON.AppState["auth"], &map[string]interface{}{
		"accounts": &authAccounts,
	}); err != nil {
		return nil, fmt.Errorf("failed to unmarshal auth accounts: %w", err)
	}

	for i := range authAccounts {
		if authAccounts[i].BaseAccount.Address == walletAddressOfInitializer {
			authAccounts[i].BaseAccount.Sequence = "0" // reset all sequences to 0
		}
		if len(authAccounts[i].Permissions) > 0 {
			hasBurner := false
			hasStaking := false
			if authAccounts[i].Permissions != nil {
				for _, perm := range authAccounts[i].Permissions {
					if perm == "burner" {
						hasBurner = true
					}
					if perm == "staking" {
						hasStaking = true
					}
				}
				if hasBurner && hasStaking {
					// Clear balances for burner+staking accounts
					for j := range bankState["balances"].([]interface{}) {
						if authAccounts[i].BaseAccount.Address == bankState["balances"].([]interface{})[j].(map[string]interface{})["address"].(string) {
							bankState["balances"].([]interface{})[j].(map[string]interface{})["coins"] = []interface{}{}
						}
					}
				}
			}
		}
	}

	// Update bank state
	bankStateBytes, err := json.Marshal(bankState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bank state: %w", err)
	}
	tinyGenesisJSON.AppState["bank"] = bankStateBytes

	tinyGenesisJSON.AppState["auth"] = genesisJSON.AppState["auth"]

	// Update hyperion state
	hyperionStateBytes, err := json.Marshal(hyperionState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal hyperion state: %w", err)
	}
	tinyGenesisJSON.AppState["hyperion"] = hyperionStateBytes

	// Update staking state
	stakingStateBytes, err := json.Marshal(map[string]interface{}{
		"params":                stakingState["params"],
		"last_total_power":      "0",
		"last_validator_powers": []interface{}{},
		"validators":            []interface{}{},
		"delegations":           []interface{}{},
		"unbonding_delegations": []interface{}{},
		"redelegations":         []interface{}{},
		"delegation_boosts":     []interface{}{},
		"exported":              false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal staking state: %w", err)
	}
	tinyGenesisJSON.AppState["staking"] = stakingStateBytes

	fmt.Println("Genesis generated")
	return &tinyGenesisJSON, nil
}

// ExportCmd dumps app state to JSON.
func ExportSoftResetCmd(appExporter types.AppExporter, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export-soft-reset",
		Short: "Export Soft Reset state to JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverCtx := GetServerContextFromCmd(cmd)
			config := serverCtx.Config

			homeDir, _ := cmd.Flags().GetString(flags.FlagHome)
			config.SetRoot(homeDir)

			if _, err := os.Stat(config.GenesisFile()); os.IsNotExist(err) {
				return err
			}

			tinyGenesis, err := os.ReadFile(config.GenesisFile())
			if err != nil {
				return err
			}

			db, err := openDB(config.RootDir, GetAppDBBackend(serverCtx.Viper))
			if err != nil {
				return err
			}

			bridgeDB, err := openBridgeDB(config.RootDir, GetAppDBBackend(serverCtx.Viper))
			if err != nil {
				return err
			}

			chronosDB, err := openChronosDB(config.RootDir, GetAppDBBackend(serverCtx.Viper))
			if err != nil {
				return err
			}

			if appExporter == nil {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: App exporter not defined. Returning genesis file."); err != nil {
					return err
				}

				// Open file in read-only mode so we can copy it to stdout.
				// It is possible that the genesis file is large,
				// so we don't need to read it all into memory
				// before we stream it out.
				f, err := os.OpenFile(config.GenesisFile(), os.O_RDONLY, 0)
				if err != nil {
					return err
				}
				defer f.Close()

				if _, err := io.Copy(cmd.OutOrStdout(), f); err != nil {
					return err
				}

				return nil
			}

			traceWriterFile, _ := cmd.Flags().GetString(flagTraceStore)
			traceWriter, err := openTraceWriter(traceWriterFile)
			if err != nil {
				return err
			}

			height, _ := cmd.Flags().GetInt64(FlagHeight)
			forZeroHeight, _ := cmd.Flags().GetBool(FlagForZeroHeight)
			jailAllowedAddrs, _ := cmd.Flags().GetStringSlice(FlagJailAllowedAddrs)
			modulesToExport, _ := cmd.Flags().GetStringSlice(FlagModulesToExport)
			outputDocument, _ := cmd.Flags().GetString(flags.FlagOutputDocument)

			exported, err := appExporter(serverCtx.Logger, db, bridgeDB, chronosDB, traceWriter, height, forZeroHeight, jailAllowedAddrs, serverCtx.Viper, modulesToExport)
			if err != nil {
				return fmt.Errorf("error exporting state: %w", err)
			}

			appGenesis, err := genutiltypes.AppGenesisFromFile(serverCtx.Config.GenesisFile())
			if err != nil {
				return err
			}

			// set current binary version
			appGenesis.AppName = version.AppName
			appGenesis.AppVersion = version.Version

			appGenesis.AppState = exported.AppState
			appGenesis.InitialHeight = exported.Height
			appGenesis.Consensus = genutiltypes.NewConsensusGenesis(exported.ConsensusParams, exported.Validators)

			// Process the genesis for soft reset
			// Convert appGenesis.AppState (json.RawMessage) to map[string]json.RawMessage
			var appStateMap map[string]json.RawMessage
			if err := json.Unmarshal(appGenesis.AppState, &appStateMap); err != nil {
				return fmt.Errorf("failed to unmarshal app state: %w", err)
			}

			genesisJSON := &Genesis{
				AppState:      appStateMap,
				InitialHeight: appGenesis.InitialHeight,
				Consensus:     json.RawMessage(`{"validators": []}`), // We'll clear this anyway
			}

			// TODO: This should be configurable or passed as a parameter
			walletAddressOfInitializer := "helios1your-initializer-address-here"

			processedGenesis, err := processGenesisSoftReset(genesisJSON, tinyGenesis, walletAddressOfInitializer)
			if err != nil {
				return fmt.Errorf("error processing genesis for soft reset: %w", err)
			}

			// Convert processed genesis back to json.RawMessage
			processedAppStateBytes, err := json.Marshal(processedGenesis.AppState)
			if err != nil {
				return fmt.Errorf("failed to marshal processed app state: %w", err)
			}

			// Update appGenesis with processed data
			appGenesis.AppState = processedAppStateBytes
			appGenesis.InitialHeight = processedGenesis.InitialHeight
			appGenesis.Consensus = genutiltypes.NewConsensusGenesis(exported.ConsensusParams, exported.Validators)

			out, err := json.Marshal(appGenesis)
			if err != nil {
				return err
			}

			if outputDocument == "" {
				// Copy the entire genesis file to stdout.
				_, err := io.Copy(cmd.OutOrStdout(), bytes.NewReader(out))
				return err
			}

			if err = appGenesis.SaveAs(outputDocument); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().Int64(FlagHeight, -1, "Export state from a particular height (-1 means latest height)")
	cmd.Flags().Bool(FlagForZeroHeight, false, "Export state to start at height zero (perform preproccessing)")
	cmd.Flags().StringSlice(FlagJailAllowedAddrs, []string{}, "Comma-separated list of operator addresses of jailed validators to unjail")
	cmd.Flags().StringSlice(FlagModulesToExport, []string{}, "Comma-separated list of modules to export. If empty, will export all modules")
	cmd.Flags().String(flags.FlagOutputDocument, "", "Exported state is written to the given file instead of STDOUT")

	return cmd
}
