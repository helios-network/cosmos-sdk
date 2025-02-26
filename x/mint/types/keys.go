package types

import (
	"cosmossdk.io/collections"
	"cosmossdk.io/math"
)

var (
	// MinterKey is the key to use for the keeper store.
	MinterKey = collections.NewPrefix(0)
	ParamsKey = collections.NewPrefix(1)
)

const (
	// module name
	ModuleName = "mint"

	// StoreKey is the default store key for mint
	StoreKey = ModuleName

	ProposalTypeUpdateInflationRate = "UpdateInflationRate"
	EventTypeInflationRateUpdate    = "inflation_rate_update"
	AttributeKeyPhase               = "phase"
	AttributeKeyNewRate             = "new_rate"

	// TokenDecimals represents the number of decimal places for HELIOS token
	HeliosDecimals = 18

	// Early stage threshold (2 billion HELIOS)
	EarlyStageThreshold = 2_000_000_000

	// Growth stage threshold (5 billion HELIOS)
	GrowthStageThreshold = 4_000_000_000

	// Mature stage threshold (10 billion HELIOS)
	MatureStageThreshold = 5_000_000_000
)

// Helper function to convert HELIOS to ahelios (base units)
func HeliosToBaseUnits(heliosAmount int64) math.Int {
	heliosInt := math.NewInt(heliosAmount)

	// Calculate 10^18 (for 18 decimal places)
	multiplier := math.NewInt(1)
	ten := math.NewInt(10)

	// Manually calculate 10^18
	for i := 0; i < 18; i++ {
		multiplier = multiplier.Mul(ten)
	}

	return heliosInt.Mul(multiplier)
}
