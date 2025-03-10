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
		k.Logger(ctx).Info("Epoch boundary reached", "epoch", currentEpoch, "height", currentHeight)

		// Get all bonded validators - fetch once and reuse
		allBondedValidators := k.GetAllBondedValidators(ctx)

		// Fetch active validators once and reuse
		currentlyActiveValidators := k.GetActiveValidatorsForCurrentEpoch(ctx)

		// Get all previously active validators across epochs
		allPreviouslyActiveValidators := k.GetAllPreviouslyActiveValidators(ctx)

		// Select validators for the new epoch using improved selection
		newEpochValidators := k.selectValidatorsForEpoch(ctx, allBondedValidators, validatorsPerEpoch)
		k.Logger(ctx).Info("Selected validators for new epoch",
			"count", len(newEpochValidators),
			"total_eligible", len(allBondedValidators))

		// Create updates for the complete validator set rotation
		updates := make([]abci.ValidatorUpdate, 0)

		// Track handled validators by a string representation of their pubkey
		handledValidators := make(map[string]bool)

		// First handle regular updates (prioritize jailing/unbonding)
		for _, update := range regularUpdates {
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

		// Track all previously active validators
		k.StoreAllPreviouslyActiveValidators(ctx, currentlyActiveValidators)

		// Store the new active validators for next epoch
		k.SetActiveValidatorsForCurrentEpoch(ctx, newEpochValidators)

		// Update epoch state
		k.SetCurrentEpoch(ctx, currentEpoch+1)
		k.SetLastEpochHeight(ctx, currentHeight)

		// Add telemetry for epoch metrics
		telemetry.SetGauge(float32(currentEpoch+1), "staking", "epoch_number")
		telemetry.SetGauge(float32(len(newEpochValidators)), "staking", "active_validators_count")

		// Log the validator rotation statistics
		continuingCount := 0
		for _, val := range newEpochValidators {
			if isValidatorInList(val, currentlyActiveValidators) {
				continuingCount++
			}
		}
		rotationPercentage := 100.0 - (float64(continuingCount) / float64(len(newEpochValidators)) * 100.0)
		k.Logger(ctx).Info("Validator rotation completed",
			"new_epoch", currentEpoch+1,
			"rotation_percentage", fmt.Sprintf("%.2f%%", rotationPercentage),
			"continuing_validators", continuingCount,
			"total_validators", len(newEpochValidators))

		// Emit epoch change event
		sdkCtx.EventManager().EmitEvent(
			sdk.NewEvent(
				"epoch_change",
				sdk.NewAttribute("epoch_number", fmt.Sprintf("%d", currentEpoch+1)),
				sdk.NewAttribute("height", fmt.Sprintf("%d", currentHeight)),
				sdk.NewAttribute("validators_count", fmt.Sprintf("%d", len(newEpochValidators))),
				sdk.NewAttribute("rotation_percentage", fmt.Sprintf("%.2f", rotationPercentage)),
			),
		)

		return updates, nil
	}

	// Not at an epoch boundary - just pass through regular updates
	// This handles mid-epoch validator changes (unbonding, jailing, etc.)
	return regularUpdates, nil
}
