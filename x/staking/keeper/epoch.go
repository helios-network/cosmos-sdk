// x/staking/keeper/epoch.go

package keeper

import (
	"context"
	"encoding/binary"
	"math/big"
	"math/rand"
	"sort"

	"cosmossdk.io/math"
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
const MaxEpochHistory = 100

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

// Generate entropy source from multiple blockchain parameters
func generateEntropySource(sdkCtx sdk.Context, currentEpoch uint64) int64 {
	blockHash := sdkCtx.HeaderHash()
	timestamp := sdkCtx.BlockHeader().Time.UnixNano()
	randSource := timestamp ^ int64(binary.BigEndian.Uint64(blockHash[:8])) ^ int64(currentEpoch)

	// Add additional entropy from last block hash if available
	if len(sdkCtx.BlockHeader().LastBlockId.Hash) >= 8 {
		randSource ^= int64(binary.BigEndian.Uint64(sdkCtx.BlockHeader().LastBlockId.Hash[:8]))
	}

	return randSource
}

// Helper function to filter non-jailed validators
func filterBondedNonJailedValidators(validators []types.Validator) []types.Validator {
	nonJailedBonded := make([]types.Validator, 0, len(validators))
	for _, val := range validators {
		if !val.IsJailed() && val.IsBonded() {
			nonJailedBonded = append(nonJailedBonded, val)
		}
	}
	return nonJailedBonded
}

// Helper type for weighted validators
type weightedValidator struct {
	validator types.Validator
	weight    *big.Int
}

// selectValidatorsForEpoch selects validators for the next epoch with improved selection strategy
func (k Keeper) selectValidatorsForEpoch(ctx context.Context, allValidators []types.Validator, count int64) []types.Validator {
	// Handle edge case: not enough validators
	if int64(len(allValidators)) <= count {
		return filterBondedNonJailedValidators(allValidators)
	}

	// Prepare context and entropy sources
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentEpoch := k.GetCurrentEpoch(ctx)
	currentValidators := k.GetActiveValidatorsForCurrentEpoch(ctx)

	// Retrieve current parameters
	params, _ := k.GetParams(sdkCtx)

	// Generate deterministic randomness
	randSource := generateEntropySource(sdkCtx, currentEpoch)
	rng := rand.New(rand.NewSource(randSource))

	// Filter non-jailed validators
	nonJailedBonded := filterBondedNonJailedValidators(allValidators)

	// Weight all validators
	weightedValidators := k.weightValidatorsWithParams(
		ctx,
		nonJailedBonded,
		currentEpoch,
		currentValidators,
		params,
	)

	// Filter out low-weight validators before selection
	filteredWeightedValidators := filterLowWeightValidators(weightedValidators)

	// If filtering removed too many validators, fall back to original list
	if len(filteredWeightedValidators) <= int(count) {
		filteredWeightedValidators = weightedValidators
	}

	// Select top validators
	return k.selectTopValidators(rng, weightedValidators, count)
}

// Comprehensive weight calculation for all validators
func (k Keeper) weightValidatorsWithParams(
	ctx context.Context,
	validators []types.Validator,
	currentEpoch uint64,
	currentValidators []types.Validator,
	params types.Params,
) []weightedValidator {
	// Calculate total validator tokens
	totalValidatorTokens := calculateTotalValidatorTokens(validators)

	var weightedValidators []weightedValidator

	for _, val := range validators {
		// Basic validator details
		tokens := val.GetTokens().BigInt()

		// Check if validator is currently active
		isCurrentValidator := isValidatorInList(val, currentValidators)

		// Calculate epochs since last active
		lastActiveEpoch := k.GetLastActiveEpoch(ctx, val.GetOperator())
		inactiveEpochs := calculateInactiveEpochs(currentEpoch, lastActiveEpoch)

		// Calculate base weight components
		// 1. Stake weight
		stakeWeight := calculateStakeWeight(tokens, totalValidatorTokens, params)

		// 2. Inactivity bonus
		inactivityBonus := calculateInactivityBonus(inactiveEpochs, params)

		// 3. Current validator bonus/penalty
		currentValidatorAdjustment := calculateCurrentValidatorAdjustment(
			isCurrentValidator,
			params,
		)

		// 4. Randomness factor
		randomBoost := calculateRandomBoost(params)

		// Combine all weight components
		finalWeight := new(big.Int).Set(stakeWeight)
		finalWeight.Add(finalWeight, inactivityBonus)
		finalWeight.Add(finalWeight, currentValidatorAdjustment)
		finalWeight.Add(finalWeight, randomBoost)

		weightedValidators = append(weightedValidators, weightedValidator{
			validator: val,
			weight:    finalWeight,
		})
	}

	// Sort by weight in descending order
	sort.Slice(weightedValidators, func(i, j int) bool {
		return weightedValidators[i].weight.Cmp(weightedValidators[j].weight) > 0
	})

	return weightedValidators
}

// HELIOS HIP - AVAILABLE IF NECESSARY - NOT USED CURRENTLY
func _calculateLogarithmicStakeWeight(
	tokens *big.Int,
	totalValidatorTokens math.Int,
	params types.Params,
) *big.Int {
	// Constants for high-precision integer math
	const (
		scaleFactor     = 1_000_000_000 // 1e9 for precision
		logBase         = 10            // Use base-10 logarithm
		logarithmFactor = 1_000_000     // Additional scaling for log precision
	)

	// Handle zero tokens case
	if tokens.Cmp(big.NewInt(0)) == 0 || totalValidatorTokens.IsZero() {
		return big.NewInt(0)
	}

	// Convert total tokens to big.Int
	totalTokensBigInt := totalValidatorTokens.BigInt()

	// Compute logarithm using integer approximation
	// log_base(x) = log(x) / log(base)

	// Step 1: Add 1 to prevent log(0) and improve scaling
	adjustedTokens := new(big.Int).Add(tokens, big.NewInt(1))

	// Step 2: Approximate logarithm using integer division
	// Use a series of integer divisions to approximate log
	logApprox := big.NewInt(0)
	currentVal := new(big.Int).Set(adjustedTokens)

	for currentVal.Cmp(big.NewInt(logBase)) > 0 {
		logApprox.Add(logApprox, big.NewInt(1))
		currentVal.Div(currentVal, big.NewInt(logBase))
	}

	// Scale the logarithmic approximation
	logScaled := new(big.Int).Mul(logApprox, big.NewInt(logarithmFactor))

	// Compute stake ratio (with scaled precision)
	scaledTokens := new(big.Int).Mul(tokens, big.NewInt(scaleFactor))
	stakeRatio := new(big.Int).Div(scaledTokens, totalTokensBigInt)

	// Combine logarithmic weight with stake ratio
	// Use params for fine-tuning
	stakeWeightFactor := big.NewInt(int64(params.StakeWeightFactor))
	if stakeWeightFactor.Cmp(big.NewInt(0)) <= 0 || stakeWeightFactor.Cmp(big.NewInt(100)) > 0 {
		stakeWeightFactor = big.NewInt(85) // Default safe value
	}

	// Combine logarithmic approximation with stake ratio
	finalWeight := new(big.Int).Mul(logScaled, stakeRatio)
	finalWeight.Div(finalWeight, big.NewInt(scaleFactor))

	// Apply stake weight factor
	finalWeight.Mul(finalWeight, stakeWeightFactor)
	finalWeight.Div(finalWeight, big.NewInt(100))

	return finalWeight
}

// Calculate stake-based weight
func calculateStakeWeight(
	tokens *big.Int,
	totalValidatorTokens math.Int,
	params types.Params,
) *big.Int {
	// Convert total tokens to big.Int
	totalTokensBigInt := totalValidatorTokens.BigInt()

	// Define scaling factor to avoid decimals
	const scaleFactor = 1_000_000_000 // 1e9 for precision scaling
	scaleFactorBigInt := big.NewInt(scaleFactor)

	// Avoid division by zero and handle zero tokens case
	if totalTokensBigInt.Cmp(big.NewInt(0)) == 0 || tokens.Cmp(big.NewInt(0)) == 0 {
		return big.NewInt(0)
	}

	// Validate stake weight factor
	stakeWeightFactor := big.NewInt(int64(params.StakeWeightFactor))
	if stakeWeightFactor.Cmp(big.NewInt(0)) <= 0 || stakeWeightFactor.Cmp(big.NewInt(100)) > 0 {
		stakeWeightFactor = big.NewInt(85) // Default safe value
	}

	// Multiply first to maintain precision, then divide
	scaledTokens := new(big.Int).Mul(tokens, scaleFactorBigInt)
	stakeRatio := new(big.Int).Div(scaledTokens, totalTokensBigInt)

	// Apply stake weight factor (scaled by 100)
	adjustedStakeWeight := new(big.Int).Mul(stakeRatio, stakeWeightFactor)
	adjustedStakeWeight.Div(adjustedStakeWeight, big.NewInt(100))

	return adjustedStakeWeight
}

// Calculate bonus for inactive validators
func calculateInactivityBonus(
	inactiveEpochs *big.Int,
	params types.Params,
) *big.Int {
	// Bonus increases with inactivity periods
	inactivityFactor := new(big.Int).Mul(
		inactiveEpochs,
		big.NewInt(int64(params.BaselineChanceFactor)),
	)
	return inactivityFactor
}

// Adjust weight based on current validator status
func calculateCurrentValidatorAdjustment(
	isCurrentValidator bool,
	params types.Params,
) *big.Int {
	if isCurrentValidator {
		// Slight bonus for current validators to ensure some stability
		return big.NewInt(int64(params.BaselineChanceFactor) * 10)
	}
	return big.NewInt(0)
}

// Add some randomness to weights
func calculateRandomBoost(params types.Params) *big.Int {
	// Generate a small random boost
	randomBoost := big.NewInt(int64(params.RandomnessFactor))
	return randomBoost
}

// Select validators using weighted randomness to ensure `count` is met
func (k Keeper) selectTopValidators(
	rng *rand.Rand,
	weightedValidators []weightedValidator,
	count int64,
) []types.Validator {
	// Ensure we don't select more than available
	if int64(len(weightedValidators)) <= count {
		return extractValidators(weightedValidators)
	}

	// Compute total weight sum
	totalWeight := big.NewInt(0)
	for _, wv := range weightedValidators {
		totalWeight.Add(totalWeight, wv.weight)
	}

	// Prepare selection pool
	selected := make([]types.Validator, 0, count)
	seen := make(map[string]struct{})
	maxAttempts := 3 * int(count) // Avoid infinite loop
	attempts := 0

	// Perform weighted random selection
	for len(selected) < int(count) && attempts < maxAttempts {
		pick := new(big.Int).Rand(rng, totalWeight) // Pick a random weight threshold
		accumWeight := big.NewInt(0)

		for _, wv := range weightedValidators {
			accumWeight.Add(accumWeight, wv.weight)
			if accumWeight.Cmp(pick) >= 0 {
				// Ensure no duplicate selection
				if _, exists := seen[wv.validator.GetOperator()]; exists {
					continue
				}

				selected = append(selected, wv.validator)
				seen[wv.validator.GetOperator()] = struct{}{}

				// Reduce totalWeight to avoid picking the same validator again
				totalWeight.Sub(totalWeight, wv.weight)
				break // Restart selection process
			}
		}
		attempts++
	}

	// If we failed to pick enough validators, return what we have
	return selected
}

// Calculate an adaptive minimum weight threshold using a percentile approach
func calculateMinWeightThreshold(weightedValidators []weightedValidator) *big.Int {
	if len(weightedValidators) == 0 {
		return big.NewInt(0)
	}

	// Extract weights and sort
	weights := make([]*big.Int, len(weightedValidators))
	for i, wv := range weightedValidators {
		weights[i] = new(big.Int).Set(wv.weight) // Prevent mutation
	}
	sort.Slice(weights, func(i, j int) bool {
		return weights[i].Cmp(weights[j]) < 0
	})

	// Define threshold as 25% percentile (more strict than median)
	percentileIndex := len(weights) / 4
	if percentileIndex < 1 {
		percentileIndex = 1
	}
	threshold := new(big.Int).Div(weights[percentileIndex], big.NewInt(5)) // Adjust to 20% of 25th percentile

	// Prevent near-zero thresholds
	minBaseThreshold := big.NewInt(1000)
	if threshold.Cmp(minBaseThreshold) < 0 {
		return minBaseThreshold
	}
	return threshold
}

// Filters out validators with extremely low weight based on dynamic threshold
func filterLowWeightValidators(weightedValidators []weightedValidator) []weightedValidator {
	minWeightThreshold := calculateMinWeightThreshold(weightedValidators)

	valid := []weightedValidator{}
	for _, wv := range weightedValidators {
		if wv.weight.Cmp(minWeightThreshold) >= 0 {
			valid = append(valid, wv)
		}
	}
	return valid
}

// Extracts only the validators from a weighted list
func extractValidators(weighted []weightedValidator) []types.Validator {
	validators := make([]types.Validator, 0, len(weighted))
	for _, wv := range weighted {
		validators = append(validators, wv.validator)
	}
	return validators
}

// Calculate inactive epochs with safe minimum
func calculateInactiveEpochs(currentEpoch, lastActiveEpoch uint64) *big.Int {
	if currentEpoch <= lastActiveEpoch {
		return big.NewInt(0) // Now correctly returns 0 if they were last active in this epoch
	}
	return big.NewInt(int64(currentEpoch - lastActiveEpoch))
}

// Helper function to calculate total validator tokens
func calculateTotalValidatorTokens(validators []types.Validator) math.Int {
	totalTokens := math.ZeroInt()
	for _, val := range validators {
		totalTokens = totalTokens.Add(val.GetTokens())
	}
	return totalTokens
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
