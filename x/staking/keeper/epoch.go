// x/staking/keeper/epoch.go

package keeper

import (
	"context"
	"encoding/binary"
	"math/rand"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Epoch-related storage keys
var (
	CurrentEpochKey            = []byte("CurrentEpoch")
	LastEpochHeightKey         = []byte("LastEpochHeight")
	ActiveEpochValidatorsKey   = []byte("ActiveEpochValidators")
	LastActiveEpochKey         = []byte{0x98} // Prefix for storing when a validator was last active
	PreviousEpochValidatorsKey = []byte{0x99} // Prefix for storing previously active validators
	EpochHistoryPrefix         = []byte{0x9A} // Prefix for storing historical epoch data
)

// MaxEpochHistory defines how many epochs of history to keep
const MaxEpochHistory = 5

// GetEpochLength returns the current epoch length in blocks
func (k Keeper) GetEpochLength(ctx context.Context) uint64 {
	params, _ := k.GetParams(ctx)
	return params.EpochLength
}

// GetValidatorsPerEpoch returns the number of validators per epoch
func (k Keeper) GetValidatorsPerEpoch(ctx context.Context) int64 {
	params, _ := k.GetParams(ctx)
	return params.ValidatorsPerEpoch
}

// IsEpochEnabled returns whether epoch-based validator rotation is enabled
func (k Keeper) IsEpochEnabled(ctx context.Context) bool {
	params, _ := k.GetParams(ctx)
	return params.EpochEnabled
}

// GetCurrentEpoch returns the current epoch number
func (k Keeper) GetCurrentEpoch(ctx context.Context) uint64 {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(CurrentEpochKey)
	if err != nil || bz == nil {
		return 0
	}
	return binary.BigEndian.Uint64(bz)
}

// SetCurrentEpoch sets the current epoch number
func (k Keeper) SetCurrentEpoch(ctx context.Context, epoch uint64) {
	store := k.storeService.OpenKVStore(ctx)
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, epoch)
	err := store.Set(CurrentEpochKey, bz)
	if err != nil {
		panic(err) // Or handle error appropriately
	}
}

// GetLastEpochHeight returns the height of the last epoch change
func (k Keeper) GetLastEpochHeight(ctx context.Context) uint64 {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(LastEpochHeightKey)
	if err != nil || bz == nil {
		return 0
	}
	return binary.BigEndian.Uint64(bz)
}

// SetLastEpochHeight sets the height of the last epoch change
func (k Keeper) SetLastEpochHeight(ctx context.Context, height uint64) {
	store := k.storeService.OpenKVStore(ctx)
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, height)
	err := store.Set(LastEpochHeightKey, bz)
	if err != nil {
		panic(err) // Or handle error appropriately
	}
}

// ActiveValidators is a struct to store active validator addresses
type ActiveValidators struct {
	Addresses []string `protobuf:"bytes,1,rep,name=addresses,proto3" json:"addresses,omitempty"`
}

// Make ActiveValidators a ProtoMsg by adding proto methods
func (m *ActiveValidators) Reset()         { *m = ActiveValidators{} }
func (m *ActiveValidators) String() string { return "ActiveValidators" }
func (m *ActiveValidators) ProtoMessage()  {}

// GetLastActiveEpoch returns the last epoch a validator was active
func (k Keeper) GetLastActiveEpoch(ctx context.Context, valAddr string) uint64 {
	store := k.storeService.OpenKVStore(ctx)
	key := append(LastActiveEpochKey, []byte(valAddr)...)
	bz, err := store.Get(key)
	if err != nil || bz == nil {
		return 0
	}
	return binary.BigEndian.Uint64(bz)
}

// SetLastActiveEpoch records when a validator was last active
func (k Keeper) SetLastActiveEpoch(ctx context.Context, valAddr string, epoch uint64) {
	store := k.storeService.OpenKVStore(ctx)
	key := append(LastActiveEpochKey, []byte(valAddr)...)
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, epoch)
	err := store.Set(key, bz)
	if err != nil {
		panic(err) // Or handle error appropriately
	}
}

// GetActiveValidatorsForCurrentEpoch returns validators active in the current epoch
func (k Keeper) GetActiveValidatorsForCurrentEpoch(ctx context.Context) []types.Validator {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(ActiveEpochValidatorsKey)
	if err != nil || bz == nil {
		return []types.Validator{}
	}

	activeValidators := ActiveValidators{}
	k.cdc.MustUnmarshal(bz, &activeValidators)

	validators := make([]types.Validator, 0, len(activeValidators.Addresses))
	for _, addrStr := range activeValidators.Addresses {
		valAddr, err := sdk.ValAddressFromBech32(addrStr)
		if err != nil {
			continue
		}

		val, err := k.GetValidator(ctx, valAddr)
		if err == nil {
			validators = append(validators, val)
		}
	}

	return validators
}

// SetActiveValidatorsForCurrentEpoch stores the active validators for the current epoch
func (k Keeper) SetActiveValidatorsForCurrentEpoch(ctx context.Context, validators []types.Validator) {
	addresses := make([]string, 0, len(validators))
	currentEpoch := k.GetCurrentEpoch(ctx)

	// Store the current epoch's validator set in history before updating
	k.storeEpochHistory(ctx, currentEpoch, validators)

	// Prune old history entries if they exceed MaxEpochHistory
	k.pruneEpochHistory(ctx, currentEpoch)

	for _, val := range validators {
		addresses = append(addresses, val.GetOperator())
		// Update the last active epoch for each validator
		k.SetLastActiveEpoch(ctx, val.GetOperator(), currentEpoch)
	}

	activeValidators := ActiveValidators{
		Addresses: addresses,
	}

	bz := k.cdc.MustMarshal(&activeValidators)
	store := k.storeService.OpenKVStore(ctx)
	err := store.Set(ActiveEpochValidatorsKey, bz)
	if err != nil {
		panic(err)
	}
}

// storeEpochHistory saves the validator set for a specific epoch
func (k Keeper) storeEpochHistory(ctx context.Context, epoch uint64, validators []types.Validator) {
	if epoch == 0 {
		return // Don't store epoch 0
	}

	store := k.storeService.OpenKVStore(ctx)

	addresses := make([]string, 0, len(validators))
	for _, val := range validators {
		addresses = append(addresses, val.GetOperator())
	}

	activeValidators := ActiveValidators{
		Addresses: addresses,
	}

	bz := k.cdc.MustMarshal(&activeValidators)

	key := makeEpochHistoryKey(epoch)
	err := store.Set(key, bz)
	if err != nil {
		k.Logger(ctx).Error("Failed to store epoch history", "epoch", epoch, "error", err)
	}
}

// pruneEpochHistory removes old epoch history entries to limit storage growth
func (k Keeper) pruneEpochHistory(ctx context.Context, currentEpoch uint64) {
	if currentEpoch <= MaxEpochHistory {
		return // Not enough history to prune
	}

	store := k.storeService.OpenKVStore(ctx)
	epochToPrune := currentEpoch - MaxEpochHistory
	key := makeEpochHistoryKey(epochToPrune)
	err := store.Delete(key)
	if err != nil {
		k.Logger(ctx).Error("Failed to prune epoch history", "epoch", epochToPrune, "error", err)
	}
}

// makeEpochHistoryKey creates a storage key for epoch history
func makeEpochHistoryKey(epoch uint64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, epoch)
	return append(EpochHistoryPrefix, bz...)
}

// GetEpochValidators retrieves validators from a specific epoch
func (k Keeper) GetEpochValidators(ctx context.Context, epoch uint64) []types.Validator {
	// For current epoch, use the active validators
	if epoch == k.GetCurrentEpoch(ctx) {
		return k.GetActiveValidatorsForCurrentEpoch(ctx)
	}

	// Otherwise retrieve from history
	store := k.storeService.OpenKVStore(ctx)
	key := makeEpochHistoryKey(epoch)
	bz, err := store.Get(key)
	if err != nil || bz == nil {
		return []types.Validator{}
	}

	activeValidators := ActiveValidators{}
	k.cdc.MustUnmarshal(bz, &activeValidators)

	validators := make([]types.Validator, 0, len(activeValidators.Addresses))
	for _, addrStr := range activeValidators.Addresses {
		valAddr, err := sdk.ValAddressFromBech32(addrStr)
		if err != nil {
			continue
		}

		val, err := k.GetValidator(ctx, valAddr)
		if err == nil {
			validators = append(validators, val)
		}
	}

	return validators
}

// GetAllPreviouslyActiveValidators gets all validators that were active in any previous epoch
func (k Keeper) GetAllPreviouslyActiveValidators(ctx context.Context) []types.Validator {
	store := k.storeService.OpenKVStore(ctx)

	var validators []types.Validator

	// Iterate through the store to find all previously active validators
	iterator, err := store.Iterator(PreviousEpochValidatorsKey, append(PreviousEpochValidatorsKey, 0xFF))
	if err != nil {
		k.Logger(ctx).Error("failed to get iterator for previous validators", "error", err)
		return validators // Return empty slice on error
	}
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		// The key is of format PreviousEpochValidatorsKey + valAddr
		// Extract the validator address part
		valAddrBytes := iterator.Key()[len(PreviousEpochValidatorsKey):]
		valAddr := sdk.ValAddress(valAddrBytes)

		// Look up the validator by address
		val, err := k.GetValidator(ctx, valAddr)
		if err == nil { // No error means we found the validator
			validators = append(validators, val)
		}
	}

	return validators
}

// StoreAllPreviouslyActiveValidators stores the currently active validators in the set of previous validators
func (k Keeper) StoreAllPreviouslyActiveValidators(ctx context.Context, validators []types.Validator) {
	store := k.storeService.OpenKVStore(ctx)

	// Clear existing entries first to avoid duplicates or stale data
	k.clearPreviouslyActiveValidators(ctx)

	// Store each validator's address in the set
	for _, val := range validators {
		// Get the validator address
		valAddr, err := sdk.ValAddressFromBech32(val.GetOperator())
		if err != nil {
			k.Logger(ctx).Error("failed to parse validator address", "addr", val.GetOperator(), "error", err)
			continue // Skip if we can't parse the address
		}

		// Store a marker - the value doesn't matter, only the key presence
		key := append(PreviousEpochValidatorsKey, valAddr...)
		err = store.Set(key, []byte{1}) // Just store a marker value
		if err != nil {
			k.Logger(ctx).Error("failed to store previous validator", "error", err)
			continue
		}
	}
}

// Helper method to clear the previously active validators set
func (k Keeper) clearPreviouslyActiveValidators(ctx context.Context) {
	store := k.storeService.OpenKVStore(ctx)

	// Iterate and delete all entries with the prefix
	iterator, err := store.Iterator(PreviousEpochValidatorsKey, append(PreviousEpochValidatorsKey, 0xFF))
	if err != nil {
		k.Logger(ctx).Error("failed to get iterator for clearing previous validators", "error", err)
		return
	}
	defer iterator.Close()

	keysToDelete := [][]byte{}
	for ; iterator.Valid(); iterator.Next() {
		keysToDelete = append(keysToDelete, iterator.Key())
	}

	// Delete outside the iterator loop for safety
	for _, key := range keysToDelete {
		if err := store.Delete(key); err != nil {
			k.Logger(ctx).Error("failed to delete previous validator key", "error", err)
		}
	}
}

// selectValidatorsForEpoch selects validators for the next epoch with all improvements
func (k Keeper) selectValidatorsForEpoch(ctx context.Context, allValidators []types.Validator, count int64) []types.Validator {
	// If we have fewer validators than needed, use all non-jailed validators
	if int64(len(allValidators)) <= count {
		selected := make([]types.Validator, 0, len(allValidators))
		for _, val := range allValidators {
			if !val.IsJailed() {
				selected = append(selected, val)
			}
		}
		return selected
	}

	// Get non-jailed validators
	nonJailed := make([]types.Validator, 0, len(allValidators))
	for _, val := range allValidators {
		if !val.IsJailed() {
			nonJailed = append(nonJailed, val)
		}
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentEpoch := k.GetCurrentEpoch(ctx)
	currentValidators := k.GetActiveValidatorsForCurrentEpoch(ctx)

	// IMPROVEMENT 1: Better randomness source
	// Mix multiple sources of entropy for improved randomness
	blockHash := sdkCtx.HeaderHash()
	timestamp := sdkCtx.BlockHeader().Time.UnixNano()
	randSource := timestamp ^ int64(binary.BigEndian.Uint64(blockHash[:8])) ^ int64(currentEpoch)

	// Add additional entropy from more recent blocks if available
	// This makes it harder to predict in advance
	if len(sdkCtx.BlockHeader().LastBlockId.Hash) >= 8 {
		randSource ^= int64(binary.BigEndian.Uint64(sdkCtx.BlockHeader().LastBlockId.Hash[:8]))
	}

	// Create a random number generator with our combined entropy sources
	rng := rand.New(rand.NewSource(randSource))

	// IMPROVEMENT 2: Ensure validator rotation - ensure at least 40% are new validators
	// First, determine how many validators we need to rotate out (at least 40%)
	minNewValidators := int64(float64(count) * 0.4)
	maxContinuingValidators := count - minNewValidators

	// Split validators into two groups: currently active and inactive
	var activeValidators []types.Validator
	var inactiveValidators []types.Validator

	for _, val := range nonJailed {
		if isValidatorInList(val, currentValidators) {
			activeValidators = append(activeValidators, val)
		} else {
			inactiveValidators = append(inactiveValidators, val)
		}
	}

	// IMPROVEMENT 3: Weighted selection for inactive validators based on stake and inactivity
	// Calculate weights for inactive validators based on stake and time since last active
	type weightedValidator struct {
		validator types.Validator
		weight    float64
	}

	// Weight inactive validators
	var weightedInactive []weightedValidator
	for _, val := range inactiveValidators {
		// Get the validator's tokens (stake)
		tokens := val.GetTokens().Int64()

		// Get the epochs since this validator was last active
		lastActiveEpoch := k.GetLastActiveEpoch(ctx, val.GetOperator())
		inactiveEpochs := uint64(1) // Minimum 1 to avoid division by zero
		if currentEpoch > lastActiveEpoch {
			inactiveEpochs = currentEpoch - lastActiveEpoch
		}

		// Calculate weight based on stake and inactivity period
		// Higher stake and longer inactivity both increase weight
		weight := float64(tokens) * float64(inactiveEpochs)

		weightedInactive = append(weightedInactive, weightedValidator{
			validator: val,
			weight:    weight,
		})
	}

	// Sort inactive validators by weight (descending)
	sort.Slice(weightedInactive, func(i, j int) bool {
		return weightedInactive[i].weight > weightedInactive[j].weight
	})

	// Build the new validator set
	selected := make([]types.Validator, 0, count)

	// First add inactive validators with highest weights to ensure rotation
	// We aim to add at least minNewValidators from inactive set
	for i := 0; i < len(weightedInactive) && int64(len(selected)) < minNewValidators; i++ {
		selected = append(selected, weightedInactive[i].validator)
	}

	// If we couldn't get enough new validators, we'll just have to use what we have
	// Now we'll add active validators, but ensure we don't exceed maxContinuingValidators

	// Shuffle the active validators for random selection
	shuffledActiveIndices := rng.Perm(len(activeValidators))

	// Add active validators but respect maxContinuingValidators limit
	activeAdded := int64(0)
	for i := 0; i < len(shuffledActiveIndices) && activeAdded < maxContinuingValidators && int64(len(selected)) < count; i++ {
		selected = append(selected, activeValidators[shuffledActiveIndices[i]])
		activeAdded++
	}

	// If we still need more validators after using up our inactive and maxContinuingValidators active ones,
	// add remaining inactive validators
	remainingInactiveIndex := minNewValidators
	for int64(len(selected)) < count && int(remainingInactiveIndex) < len(weightedInactive) {
		selected = append(selected, weightedInactive[remainingInactiveIndex].validator)
		remainingInactiveIndex++
	}

	// If we somehow still don't have enough (unlikely), we can exceed our maxContinuingValidators
	// limit and add more active validators
	for i := int(activeAdded); int64(len(selected)) < count && i < len(shuffledActiveIndices); i++ {
		selected = append(selected, activeValidators[shuffledActiveIndices[i]])
	}

	// Final shuffle of the entire selected set to randomize positions
	finalIndices := rng.Perm(len(selected))
	finalSelected := make([]types.Validator, 0, len(selected))
	for _, idx := range finalIndices {
		finalSelected = append(finalSelected, selected[idx])
	}

	return finalSelected
}

// isValidatorInList checks if a validator is in the given list
func isValidatorInList(validator types.Validator, validators []types.Validator) bool {
	valAddr := validator.GetOperator() // GetOperator returns a string

	for _, v := range validators {
		if valAddr == v.GetOperator() { // Compare strings directly
			return true
		}
	}

	return false
}

// GetAllBondedValidators returns all bonded validators
func (k Keeper) GetAllBondedValidators(ctx context.Context) []types.Validator {
	validators := []types.Validator{}
	err := k.IterateBondedValidatorsByPower(ctx, func(index int64, validator types.ValidatorI) (stop bool) {
		validators = append(validators, validator.(types.Validator))
		return false
	})

	if err != nil {
		// Handle error - for simplicity just return empty list
		return []types.Validator{}
	}

	return validators
}

// GETTERS FOR GRPC

// GetCurrentEpochValidators returns the validators in the current epoch
// This is a wrapper around GetActiveValidatorsForCurrentEpoch that returns the result
// in the format expected by the gRPC query handler
func (k Keeper) GetCurrentEpochValidators(ctx context.Context) []types.Validator {
	return k.GetActiveValidatorsForCurrentEpoch(ctx)
}

// GetCurrentEpochNumber is a wrapper for GetCurrentEpoch to maintain compatibility
func (k Keeper) GetCurrentEpochNumber(ctx context.Context) uint64 {
	return k.GetCurrentEpoch(ctx)
}

// GetPreviousEpochValidators returns the validators from the previous epoch
// This is a wrapper around GetAllPreviouslyActiveValidators that returns the result
// in the format expected by the gRPC query handler
func (k Keeper) GetPreviousEpochValidators(ctx context.Context) []types.Validator {
	return k.GetAllPreviouslyActiveValidators(ctx)
}
