package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"
)

// Keys for store prefixes
var (
	EarlyPhaseInflationRateKey  = []byte("EarlyPhaseInflationRate")  // 0-2B supply
	GrowthPhaseInflationRateKey = []byte("GrowthPhaseInflationRate") // 2B-4B supply
	MaturePhaseInflationRateKey = []byte("MaturePhaseInflationRate") // 4B-5B supply
	PostCapInflationRateKey     = []byte("PostCapInflationRate")     // >5B supply
)

// GetEarlyPhaseInflationRate returns the inflation rate for 0-2B supply
func (k Keeper) GetEarlyPhaseInflationRate(ctx context.Context) (math.LegacyDec, error) {
	return k.getInflationRate(ctx, EarlyPhaseInflationRateKey, math.LegacyNewDecWithPrec(15, 2)) // Default 15%
}

// GetGrowthPhaseInflationRate returns the inflation rate for 2B-4B supply
func (k Keeper) GetGrowthPhaseInflationRate(ctx context.Context) (math.LegacyDec, error) {
	return k.getInflationRate(ctx, GrowthPhaseInflationRateKey, math.LegacyNewDecWithPrec(12, 2)) // Default 12%
}

// GetMaturePhaseInflationRate returns the inflation rate for 4B-5B supply
func (k Keeper) GetMaturePhaseInflationRate(ctx context.Context) (math.LegacyDec, error) {
	return k.getInflationRate(ctx, MaturePhaseInflationRateKey, math.LegacyNewDecWithPrec(5, 2)) // Default 5%
}

// GetPostCapInflationRate returns the inflation rate for >5B supply
func (k Keeper) GetPostCapInflationRate(ctx context.Context) (math.LegacyDec, error) {
	return k.getInflationRate(ctx, PostCapInflationRateKey, math.LegacyNewDecWithPrec(1, 2)) // Default 1%
}

// Helper method to get inflation rate with default value
func (k Keeper) getInflationRate(ctx context.Context, key []byte, defaultRate math.LegacyDec) (math.LegacyDec, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(key)
	if err != nil {
		return defaultRate, err
	}

	// If not set, return default
	if bz == nil {
		return defaultRate, nil
	}

	// Convert bytes to string and then to LegacyDec
	rateStr := string(bz)
	rate, err := math.LegacyNewDecFromStr(rateStr)
	if err != nil {
		return defaultRate, err
	}

	return rate, nil
}

// SetEarlyPhaseInflationRate sets the inflation rate for 0-2B supply
func (k Keeper) SetEarlyPhaseInflationRate(ctx context.Context, rate math.LegacyDec) error {
	return k.setInflationRate(ctx, EarlyPhaseInflationRateKey, rate)
}

// SetGrowthPhaseInflationRate sets the inflation rate for 2B-4B supply
func (k Keeper) SetGrowthPhaseInflationRate(ctx context.Context, rate math.LegacyDec) error {
	return k.setInflationRate(ctx, GrowthPhaseInflationRateKey, rate)
}

// SetMaturePhaseInflationRate sets the inflation rate for 4B-5B supply
func (k Keeper) SetMaturePhaseInflationRate(ctx context.Context, rate math.LegacyDec) error {
	return k.setInflationRate(ctx, MaturePhaseInflationRateKey, rate)
}

// SetPostCapInflationRate sets the inflation rate for >5B supply
func (k Keeper) SetPostCapInflationRate(ctx context.Context, rate math.LegacyDec) error {
	return k.setInflationRate(ctx, PostCapInflationRateKey, rate)
}

// Helper method to set inflation rate with validation
func (k Keeper) setInflationRate(ctx context.Context, key []byte, rate math.LegacyDec) error {
	// Validate the inflation rate is within reasonable limits (0-100%)
	if rate.LT(math.LegacyZeroDec()) || rate.GT(math.LegacyNewDecWithPrec(100, 2)) {
		return fmt.Errorf("invalid inflation rate: must be between 0%% and 100%%")
	}

	store := k.storeService.OpenKVStore(ctx)

	// Convert rate to string and store as bytes
	rateStr := rate.String()
	return store.Set(key, []byte(rateStr))
}
