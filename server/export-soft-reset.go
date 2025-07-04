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
	Consensus     json.RawMessage            `json:"consensus"`
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
	BaseAccount BaseAccount `json:"base_account"`
	Permissions []string    `json:"permissions,omitempty"`
}

type BaseAccount struct {
	Address  string `json:"address"`
	Sequence string `json:"sequence"`
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
	var stakingState StakingState
	if err := json.Unmarshal(genesisJSON.AppState["staking"], &stakingState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal staking state: %w", err)
	}

	// Parse bank state
	var bankBalances []BankBalance
	if err := json.Unmarshal(genesisJSON.AppState["bank"], &map[string]interface{}{
		"balances": &bankBalances,
	}); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bank state: %w", err)
	}

	// Add boost balances back to the delegator
	for _, delegationBoost := range stakingState.DelegationBoosts {
		balance := findBalance(bankBalances, delegationBoost.DelegatorAddress)
		if balance != nil {
			aheliosBalance := findCoin(balance.Coins, "ahelios")
			if aheliosBalance != nil {
				aheliosBalance.Amount = addStringAmounts(aheliosBalance.Amount, delegationBoost.Amount)
			} else {
				balance.Coins = append(balance.Coins, Coin{
					Denom:  "ahelios",
					Amount: delegationBoost.Amount,
				})
			}
		} else {
			bankBalances = append(bankBalances, BankBalance{
				Address: delegationBoost.DelegatorAddress,
				Coins: []Coin{{
					Denom:  "ahelios",
					Amount: delegationBoost.Amount,
				}},
			})
		}
	}

	// Remove delegation boosts
	stakingState.DelegationBoosts = []DelegationBoost{}

	fmt.Println("Adding delegation balances back to the delegator")
	// Add the delegation to the balance of the delegator
	for _, delegation := range stakingState.Delegations {
		balance := findBalance(bankBalances, delegation.DelegatorAddress)
		if balance != nil {
			for _, assetWeight := range delegation.AssetWeights {
				existingAssetWeight := findCoin(balance.Coins, assetWeight.Denom)
				if existingAssetWeight == nil {
					balance.Coins = append(balance.Coins, Coin{
						Denom:  assetWeight.Denom,
						Amount: assetWeight.BaseAmount,
					})
				} else {
					existingAssetWeight.Amount = addStringAmounts(existingAssetWeight.Amount, assetWeight.BaseAmount)
				}
			}
		} else {
			coins := make([]Coin, len(delegation.AssetWeights))
			for i, assetWeight := range delegation.AssetWeights {
				coins[i] = Coin{
					Denom:  assetWeight.Denom,
					Amount: assetWeight.BaseAmount,
				}
			}
			bankBalances = append(bankBalances, BankBalance{
				Address: delegation.DelegatorAddress,
				Coins:   coins,
			})
		}
	}

	stakingState.Validators = []interface{}{}
	stakingState.Delegations = []Delegation{}

	fmt.Println("Removing distribution and staking pool balances")
	// Remove specific module balances
	for i := range bankBalances {
		if bankBalances[i].Address == "helios1jv65s3grqf6v6jl3dp4t6c9t9rk99cd8nte205" { // distribution module
			bankBalances[i].Coins = []Coin{}
		}
		if bankBalances[i].Address == "helios1fl48vsnmsdzcv85q5d2q4z5ajdha8yu3p05elu" { // staking pool
			bankBalances[i].Coins = []Coin{}
		}
		if bankBalances[i].Address == "helios13c59hc2zmcrzzxfgh0umpf077cz86pytvzxda6" { // boosted pool
			bankBalances[i].Coins = []Coin{}
		}
	}

	fmt.Println("Removing hyperion sub states (batches, unbatched_transfers, batch_confirms)")
	// Parse hyperion state
	var hyperionState HyperionState
	if err := json.Unmarshal(genesisJSON.AppState["hyperion"], &hyperionState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hyperion state: %w", err)
	}

	for i := range hyperionState.SubStates {
		if len(hyperionState.SubStates[i].Batches) > 0 {
			// TODO: Manage send back to the sender
			hyperionState.SubStates[i].Batches = []interface{}{}
		}
		if len(hyperionState.SubStates[i].UnbatchedTransfers) > 0 {
			hyperionState.SubStates[i].UnbatchedTransfers = []interface{}{}
			// TODO: Manage send back to the sender
		}
		if len(hyperionState.SubStates[i].BatchConfirms) > 0 {
			hyperionState.SubStates[i].BatchConfirms = []interface{}{}
			// TODO: Manage send back to the sender
		}
	}

	// Clear consensus validators
	genesisJSON.Consensus = json.RawMessage(`{"validators": []}`)

	fmt.Println("Sorting balances by denom and removing 0 balances")
	// Sort by denom and remove 0 balances
	for i := range bankBalances {
		// Filter out 0 balances
		var filteredCoins []Coin
		for _, coin := range bankBalances[i].Coins {
			if isAmountGreaterThan(coin.Amount, "0") {
				filteredCoins = append(filteredCoins, coin)
			}
		}
		bankBalances[i].Coins = filteredCoins

		// Sort by denom
		sort.Slice(bankBalances[i].Coins, func(j, k int) bool {
			return bankBalances[i].Coins[j].Denom < bankBalances[i].Coins[k].Denom
		})
	}

	// Remove balances with little ahelios and no other coins
	var filteredBalances []BankBalance
	for _, balance := range bankBalances {
		aheliosBalance := findCoin(balance.Coins, "ahelios")
		if aheliosBalance != nil {
			if len(balance.Coins) > 1 {
				filteredBalances = append(filteredBalances, balance)
			} else if isAmountGreaterThan(aheliosBalance.Amount, "1000000000000000000") {
				filteredBalances = append(filteredBalances, balance)
			}
		} else {
			filteredBalances = append(filteredBalances, balance)
		}
	}
	bankBalances = filteredBalances

	fmt.Println("Merging genesis with tiny genesis")
	// Parse tiny genesis
	var tinyGenesisJSON Genesis
	if err := json.Unmarshal(tinyGenesis, &tinyGenesisJSON); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tiny genesis: %w", err)
	}

	tinyGenesisJSON.InitialHeight = genesisJSON.InitialHeight

	// Update bank state
	bankState := map[string]interface{}{
		"balances": bankBalances,
	}
	bankStateBytes, err := json.Marshal(bankState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bank state: %w", err)
	}
	tinyGenesisJSON.AppState["bank"] = bankStateBytes

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
				for j := range bankBalances {
					if authAccounts[i].BaseAccount.Address == bankBalances[j].Address {
						bankBalances[j].Coins = []Coin{}
					}
				}
			}
		}
	}

	authState := map[string]interface{}{
		"accounts": authAccounts,
	}
	authStateBytes, err := json.Marshal(authState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth state: %w", err)
	}
	tinyGenesisJSON.AppState["auth"] = authStateBytes

	// Update hyperion state
	hyperionStateBytes, err := json.Marshal(hyperionState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal hyperion state: %w", err)
	}
	tinyGenesisJSON.AppState["hyperion"] = hyperionStateBytes

	// Update staking state
	stakingStateBytes, err := json.Marshal(stakingState)
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

			exported, err := appExporter(serverCtx.Logger, db, traceWriter, height, forZeroHeight, jailAllowedAddrs, serverCtx.Viper, modulesToExport)
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
