package keeper

import (
	"context"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// BeginBlocker will persist the current header and validator set as a historical entry
// and prune the oldest entry based on the HistoricalEntries parameter
func (k *Keeper) BeginBlocker(ctx context.Context) error {
	defer telemetry.ModuleMeasureSince(types.ModuleName, telemetry.Now(), telemetry.MetricKeyBeginBlocker)
	return k.TrackHistoricalInfo(ctx)
}

// EndBlocker called at every block, update validator set
func (k *Keeper) EndBlocker(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	defer telemetry.ModuleMeasureSince(types.ModuleName, telemetry.Now(), telemetry.MetricKeyEndBlocker)

	// Get regular validator updates from standard staking logic
	regularUpdates, err := k.BlockValidatorUpdates(ctx)
	if err != nil {
		return nil, err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := uint64(sdkCtx.BlockHeight())

	// Check if epoch mechanism is enabled
	if !k.IsEpochEnabled(ctx) {
		return regularUpdates, nil
	}

	// Get epoch configuration
	epochLength := k.GetEpochLength(ctx)
	validatorsPerEpoch := k.GetValidatorsPerEpoch(ctx)
	lastEpochHeight := k.GetLastEpochHeight(ctx)

	// Check if we're at an epoch boundary
	if lastEpochHeight == 0 || currentHeight-lastEpochHeight >= epochLength {
		// We're at an epoch boundary - complete validator set rotation
		currentEpoch := k.GetCurrentEpoch(ctx)

		// Get all bonded validators - fetch once and reuse
		allBondedValidators := k.GetAllBondedValidators(ctx)

		// IMPROVEMENT 2: Fetch active validators once and reuse
		currentlyActiveValidators := k.GetActiveValidatorsForCurrentEpoch(ctx)

		// IMPROVEMENT 1: Get all previously active validators across epochs
		allPreviouslyActiveValidators := k.GetAllPreviouslyActiveValidators(ctx)

		// Select validators for the new epoch using improved selection
		newEpochValidators := k.selectValidatorsForEpoch(ctx, allBondedValidators, validatorsPerEpoch)

		// Create updates for the complete validator set rotation
		updates := make([]abci.ValidatorUpdate, 0)

		// IMPROVEMENT 3: Track handled validators by operator address, not pubkey string
		handledValidators := make(map[string]bool)

		// First handle regular updates (prioritize jailing/unbonding)
		for _, update := range regularUpdates {
			// Use a simple string representation of the pubkey as a unique identifier
			pubkeyStr := fmt.Sprintf("%v", update.PubKey)

			updates = append(updates, update)
			handledValidators[pubkeyStr] = true
		}

		// Deactivate ALL previously active validators not selected for new epoch
		for _, val := range allPreviouslyActiveValidators {
			// Skip if validator is in the new epoch set
			if isValidatorInList(val, newEpochValidators) {
				continue
			}

			// Deactivate validator
			update := val.ABCIValidatorUpdateZero()
			pubkeyStr := fmt.Sprintf("%v", update.PubKey)

			// Skip if already handled
			if handledValidators[pubkeyStr] {
				continue
			}

			updates = append(updates, update)
			handledValidators[pubkeyStr] = true
		}

		// Activate validators selected for new epoch
		for _, val := range newEpochValidators {
			// Skip jailed validators
			if val.IsJailed() {
				continue
			}

			// Create an ABCI validator update with proper power
			tokens := val.GetTokens()
			update := val.ABCIValidatorUpdate(tokens)
			pubkeyStr := fmt.Sprintf("%v", update.PubKey)

			// Skip if already handled
			if handledValidators[pubkeyStr] {
				continue
			}

			updates = append(updates, update)
			handledValidators[pubkeyStr] = true
		}

		// IMPROVEMENT: Track all previously active validators
		k.StoreAllPreviouslyActiveValidators(ctx, currentlyActiveValidators)

		// Store the new active validators for next epoch
		k.SetActiveValidatorsForCurrentEpoch(ctx, newEpochValidators)

		// Update epoch state
		k.SetCurrentEpoch(ctx, currentEpoch+1)
		k.SetLastEpochHeight(ctx, currentHeight)

		// Emit epoch change event
		sdkCtx.EventManager().EmitEvent(
			sdk.NewEvent(
				"epoch_change",
				sdk.NewAttribute("epoch_number", fmt.Sprintf("%d", currentEpoch+1)),
				sdk.NewAttribute("height", fmt.Sprintf("%d", currentHeight)),
				sdk.NewAttribute("validators_count", fmt.Sprintf("%d", len(newEpochValidators))),
			),
		)

		return updates, nil
	}

	// Not at an epoch boundary - just pass through regular updates
	// This handles mid-epoch validator changes (unbonding, jailing, etc.)
	return regularUpdates, nil
}
