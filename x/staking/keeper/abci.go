package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	cometcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
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

// EndBlocker executes at every block to manage validator set changes
func (k *Keeper) EndBlocker(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	defer telemetry.ModuleMeasureSince(types.ModuleName, telemetry.Now(), telemetry.MetricKeyEndBlocker)

	// Get validator updates from standard staking logic
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
	currentEpoch := k.GetCurrentEpoch(ctx)

	// Retrieve current epoch validators
	currentEpochValidators := k.GetActiveValidatorsForCurrentEpoch(ctx)

	// Remove Jailed/Unbonded Validators While Keeping Active Ones
	updates := []abci.ValidatorUpdate{}
	// New list of active validators (after removals)
	filteredCurrentEpochValidators := []types.Validator{}

	currentEpochValidatorsSet := make(map[string]struct{})

	// Precompute consensus addresses of current epoch validators for O(1) lookups
	for _, val := range currentEpochValidators {
		consAddrBytes, err := val.GetConsAddr()
		if err != nil || len(consAddrBytes) == 0 {
			k.Logger(ctx).Error("Validator missing consensus address", "operator", val.GetOperator(), "error", err)
			continue
		}
		currentEpochValidatorsSet[string(consAddrBytes)] = struct{}{}
	}

	for _, validator := range currentEpochValidators {
		// Remove jailed/unbonded validators
		if validator.IsJailed() || validator.GetStatus() != types.Bonded {
			updates = append(updates, validator.ABCIValidatorUpdateZero()) // Remove from Tendermint
		} else {
			consAddrBytes, err := validator.GetConsAddr()
			if err != nil || len(consAddrBytes) == 0 {
				k.Logger(ctx).Error("Validator missing consensus address", "operator", validator.GetOperator(), "error", err)
				continue
			}

			foundUpdate, ok := findValidatorUpdate(regularUpdates, consAddrBytes)
			if ok {
				updates = append(updates, *foundUpdate)
			}
			// Keep active validators
			filteredCurrentEpochValidators = append(filteredCurrentEpochValidators, validator)
		}
	}
	// save in store removed validators
	k.SetActiveValidatorsForCurrentEpoch(ctx, filteredCurrentEpochValidators)

	// Fetch all bonded validators
	allBondedValidators := k.GetAllBondedValidators(ctx)
	filteredNewCandidates := k.filterNewValidators(allBondedValidators, filteredCurrentEpochValidators)
	isEpochBoundary := lastEpochHeight != 0 && currentHeight-lastEpochHeight >= epochLength

	// No new validators and they are all already in current epoch, no need for extra compute
	if len(filteredCurrentEpochValidators) == len(allBondedValidators) {
		if isEpochBoundary {
			k.Logger(ctx).Info("Ignored epoch boundary as current signers already contain all existing active validators, no rotation necessary",
				"active_validators", len(filteredCurrentEpochValidators),
				"current_epoch", currentEpoch,
				"current_height", currentHeight,
			)
			k.SetLastEpochHeight(ctx, currentHeight)
			k.SetCurrentEpoch(ctx, currentEpoch+1)
		}
		return updates, nil
	}

	// Send validators update if not epoch boundary
	if !isEpochBoundary {
		// if count active validators is less then validators_per_epoch we add any available validators no matter if not in boundary
		if len(filteredCurrentEpochValidators) < int(validatorsPerEpoch) && len(filteredNewCandidates) > 0 {
			// fetch all active bonded validators
			// Add new candidates to the list of active validators
			filteredCurrentEpochValidators = append(filteredCurrentEpochValidators, filteredNewCandidates...)

			// Update the power of new validators for Tendermint
			for _, newValidator := range filteredNewCandidates {
				updates = append(updates, newValidator.ABCIValidatorUpdate(newValidator.GetTokens()))
			}

			k.Logger(ctx).Info("Automatically added new validators in non-boundary epoch due to insufficient active validators", "new_validators",
				len(filteredNewCandidates), "current_height", currentHeight, "target_validators", validatorsPerEpoch)
			k.SetActiveValidatorsForCurrentEpoch(ctx, filteredCurrentEpochValidators)
			// in case no epoch past already we have to set it for initialization
			if currentHeight == 0 {
				k.SetLastEpochHeight(ctx, currentHeight)
			}
		}
		return updates, nil
	}

	// ==== Epoch boundary reached, perform ROLLING rotation ====
	k.Logger(ctx).Info("Epoch boundary reached", "epoch", currentEpoch, "height", currentHeight)

	// Sort validators by stake (lowest first for fair removal)
	sort.Slice(filteredCurrentEpochValidators, func(i, j int) bool {
		return filteredCurrentEpochValidators[i].GetTokens().LT(filteredCurrentEpochValidators[j].GetTokens())
	})

	// Define the number of validators to replace per epoch
	rotationPercentage := 33 // keep safe 2/3 of the tendermint knowns signers
	numToReplace := int(math.Ceil(float64(len(filteredCurrentEpochValidators)*rotationPercentage) / 100))

	// If we have fewer than `validatorsPerEpoch`, fill up instead of rotating
	availableSlots := int(validatorsPerEpoch) - len(filteredCurrentEpochValidators)
	if availableSlots > 0 {
		numToReplace = availableSlots // Instead of replacing, we add new validators
		k.Logger(ctx).Info("Filling available slots with new validators", "available_slots", availableSlots)
	}

	newEpochValidators := k.selectValidatorsForEpoch(ctx, filteredNewCandidates, int64(numToReplace))
	if len(filteredCurrentEpochValidators) == 0 {
		// First epoch case, no validators set, so directly use new candidates
		k.Logger(ctx).Info("No active validators found, initializing first epoch set")

		if len(newEpochValidators) == 0 {
			k.Logger(ctx).Error("No validators available to initialize the first epoch!")
			return nil, fmt.Errorf("failed to initialize first epoch: no available validators")
		}

		k.SetActiveValidatorsForCurrentEpoch(ctx, newEpochValidators)
		k.SetCurrentEpoch(ctx, 1)
		k.SetLastEpochHeight(ctx, currentHeight)

		return updates, nil // Return updates early since it's the first initialization
	}

	// Select validators to remove and replace
	if len(newEpochValidators) < numToReplace {
		k.Logger(ctx).Error("Insufficient new validators selected, retaining existing ones",
			"required", numToReplace, "selected", len(newEpochValidators))

		// Reduce the number of validators to remove to match the available new validators
		numToReplace = len(newEpochValidators)
	}
	// Ensure at least 1 validator rotates if possible
	if numToReplace < 1 && len(filteredCurrentEpochValidators) > 1 {
		numToReplace = 1
	}

	// Ensure we never exceed the `validatorsPerEpoch` limit
	if numToReplace > int(validatorsPerEpoch) {
		numToReplace = int(validatorsPerEpoch)
	}

	var validatorsToRemove []types.Validator
	if len(filteredCurrentEpochValidators) > numToReplace {
		// Only remove validators if we have enough to remove
		validatorsToRemove = filteredCurrentEpochValidators[:numToReplace]
	} else {
		// If we don't have enough validators to remove, don't remove any
		validatorsToRemove = []types.Validator{}
	}

	// Apply validator rotation
	for _, val := range validatorsToRemove {

		consAddrBytes, err := val.GetConsAddr()
		if err != nil || len(consAddrBytes) == 0 {
			k.Logger(ctx).Error("Validator missing consensus address", "operator", val.GetOperator(), "error", err)
			continue
		}

		updates = removeExistingUpdate(updates, consAddrBytes)
		updates = append(updates, val.ABCIValidatorUpdateZero())
	}

	for _, val := range newEpochValidators {
		if val.IsJailed() {
			continue
		}
		consAddrBytes, err := val.GetConsAddr()
		if err != nil || len(consAddrBytes) == 0 {
			k.Logger(ctx).Error("Validator missing consensus address", "operator", val.GetOperator(), "error", err)
			continue
		}

		updates = removeExistingUpdate(updates, consAddrBytes)
		updates = append(updates, val.ABCIValidatorUpdate(val.GetTokens()))
	}

	// Store the updated active validator set
	var finalUpdatedValidatorSet []types.Validator
	if numToReplace > 0 && numToReplace <= len(filteredCurrentEpochValidators) {
		finalUpdatedValidatorSet = append(filteredCurrentEpochValidators[numToReplace:], newEpochValidators...)
	} else {
		finalUpdatedValidatorSet = append(filteredCurrentEpochValidators, newEpochValidators...)
	}
	k.SetActiveValidatorsForCurrentEpoch(ctx, finalUpdatedValidatorSet)
	k.SetCurrentEpoch(ctx, currentEpoch+1)
	k.SetLastEpochHeight(ctx, currentHeight)

	// Emit telemetry and logging
	telemetry.SetGauge(float32(currentEpoch+1), "staking", "epoch_number")
	telemetry.SetGauge(float32(len(finalUpdatedValidatorSet)), "staking", "active_validators_count")

	k.Logger(ctx).Info("Rolling validator rotation completed",
		"new_epoch", currentEpoch+1,
		"validators_replaced", numToReplace,
		"total_validators", len(finalUpdatedValidatorSet),
	)

	// Step 9: Emit epoch change event
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			"epoch_change",
			sdk.NewAttribute("epoch_number", fmt.Sprintf("%d", currentEpoch+1)),
			sdk.NewAttribute("height", fmt.Sprintf("%d", currentHeight)),
			sdk.NewAttribute("validators_replaced", fmt.Sprintf("%d", numToReplace)),
			sdk.NewAttribute("total_validators", fmt.Sprintf("%d", len(finalUpdatedValidatorSet))),
		),
	)

	return updates, nil
}

// Helper function to find a validator update in regularUpdates
func findValidatorUpdate(regularUpdates []abci.ValidatorUpdate, consAddrBytes []byte) (*abci.ValidatorUpdate, bool) {
	for _, update := range regularUpdates {
		pubKeyBytes, err := getConsensusAddressFromPubKey(update.PubKey)
		if err != nil {
			continue // Skip invalid keys
		}

		// Convert public key to consensus address
		if string(pubKeyBytes) == string(consAddrBytes) {
			return &update, true
		}
	}
	return nil, false // Validator update not found
}

// Correct function to extract the **Tendermint Consensus Address**
func getConsensusAddressFromPubKey(pubKey cometcrypto.PublicKey) ([]byte, error) {
	// Extract raw public key bytes
	pubKeyBytes, err := extractPubKeyBytes(pubKey)
	if err != nil {
		return nil, err
	}

	// Tendermint consensus address = First 20 bytes of SHA-256(pubKeyBytes)
	hash := sha256.Sum256(pubKeyBytes)
	return hash[:20], nil // First 20 bytes are the consensus address
}

// Keep your original function (but it's only used for extracting raw public keys)
func extractPubKeyBytes(pubKey cometcrypto.PublicKey) ([]byte, error) {
	switch key := pubKey.Sum.(type) {
	case *cometcrypto.PublicKey_Ed25519:
		return key.Ed25519, nil
	case *cometcrypto.PublicKey_Secp256K1:
		return key.Secp256K1, nil
	default:
		return nil, fmt.Errorf("unsupported public key type")
	}
}

func removeExistingUpdate(updates []abci.ValidatorUpdate, consAddrBytes []byte) []abci.ValidatorUpdate {
	filteredUpdates := make([]abci.ValidatorUpdate, 0, len(updates))
	for _, update := range updates {
		updateConsAddrBytes, err := getConsensusAddressFromPubKey(update.PubKey)
		if err != nil {
			filteredUpdates = append(filteredUpdates, update)
			continue
		}

		if !bytes.Equal(updateConsAddrBytes, consAddrBytes) {
			filteredUpdates = append(filteredUpdates, update)
		}
	}
	return filteredUpdates
}
