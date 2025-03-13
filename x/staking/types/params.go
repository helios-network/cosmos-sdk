package types

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Staking params default values
const (
	// DefaultUnbondingTime reflects 5 seconds
	// unbonding time.
	// TODO: Justify our choice of default here.
	DefaultUnbondingTime time.Duration = time.Second * 5

	// Default maximum number of bonded validators
	DefaultMaxValidators uint32 = 100

	// Default maximum entries in a UBD/RED pair
	DefaultMaxEntries uint32 = 7

	// DefaultHistoricalEntries is 10000. Apps that don't use IBC can ignore this
	// value by not adding the staking module to the application module manager's
	// SetOrderBeginBlockers.
	DefaultHistoricalEntries uint32 = 10000

	// `enabled`: Whether the Delegator Stake Reduction mechanism is active
	DefaultDelegatorStakeReductionEnabled bool = true

	// Default values for new validator selection parameters
	DefaultStakeWeightFactor    uint64 = 85 // 85% weight factor
	DefaultBaselineChanceFactor uint64 = 5  // 5% baseline chance
	DefaultRandomnessFactor     uint64 = 10 // 10% randomness influence
)

var (
	// DefaultMinCommissionRate is set to 0%
	DefaultMinCommissionRate = math.LegacyZeroDec()

	// `dominance_threshold`: Percentage threshold above which reduction begins
	DefaultDelegatorStakeReductionDominanceThreshold math.LegacyDec = math.LegacyMustNewDecFromStr("0.05")

	// `max_reduction`: Maximum possible reduction percentage
	DefaultDelegatorStakeReductionMaxReduction math.LegacyDec = math.LegacyMustNewDecFromStr("0.90")

	// `curve_steepness`: Controls how quickly reduction increases
	DefaultDelegatorStakeReductionCurveSteepness math.LegacyDec = math.LegacyMustNewDecFromStr("10.0")
)

// NewParams creates a new Params instance
func NewParams(
	unbondingTime time.Duration,
	maxValidators, maxEntries, historicalEntries uint32,
	bondDenom string,
	minCommissionRate math.LegacyDec,
	delegatorStakeReduction *StakeReductionParams,
	// New parameters
	stakeWeightFactor, baselineChanceFactor, randomnessFactor uint64,
) Params {
	return Params{
		UnbondingTime:           unbondingTime,
		MaxValidators:           maxValidators,
		MaxEntries:              maxEntries,
		HistoricalEntries:       historicalEntries,
		BondDenom:               bondDenom,
		MinCommissionRate:       minCommissionRate,
		DelegatorStakeReduction: delegatorStakeReduction,
		StakeWeightFactor:       stakeWeightFactor,
		BaselineChanceFactor:    baselineChanceFactor,
		RandomnessFactor:        randomnessFactor,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		DefaultUnbondingTime,
		DefaultMaxValidators,
		DefaultMaxEntries,
		DefaultHistoricalEntries,
		sdk.DefaultBondDenom,
		DefaultMinCommissionRate,
		&StakeReductionParams{
			Enabled:            true,
			DominanceThreshold: DefaultDelegatorStakeReductionDominanceThreshold,
			MaxReduction:       DefaultDelegatorStakeReductionMaxReduction,
			CurveSteepness:     DefaultDelegatorStakeReductionCurveSteepness,
		},
		// Add default values for new parameters
		DefaultStakeWeightFactor,
		DefaultBaselineChanceFactor,
		DefaultRandomnessFactor,
	)
}

// Validate a set of params
func (p Params) Validate() error {
	if err := validateUnbondingTime(p.UnbondingTime); err != nil {
		return err
	}

	if err := validateMaxValidators(p.MaxValidators); err != nil {
		return err
	}

	if err := validateMaxEntries(p.MaxEntries); err != nil {
		return err
	}

	if err := validateBondDenom(p.BondDenom); err != nil {
		return err
	}

	if err := validateMinCommissionRate(p.MinCommissionRate); err != nil {
		return err
	}

	if err := validateHistoricalEntries(p.HistoricalEntries); err != nil {
		return err
	}

	// Validate new parameters
	if err := validateStakeWeightFactor(p.StakeWeightFactor); err != nil {
		return err
	}

	if err := validateBaselineChanceFactor(p.BaselineChanceFactor); err != nil {
		return err
	}

	if err := validateRandomnessFactor(p.RandomnessFactor); err != nil {
		return err
	}

	return nil
}

func validateUnbondingTime(i interface{}) error {
	v, ok := i.(time.Duration)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v <= 0 {
		return fmt.Errorf("unbonding time must be positive: %d", v)
	}

	return nil
}

func validateMaxValidators(i interface{}) error {
	v, ok := i.(uint32)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v == 0 {
		return fmt.Errorf("max validators must be positive: %d", v)
	}

	return nil
}

func validateMaxEntries(i interface{}) error {
	v, ok := i.(uint32)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v == 0 {
		return fmt.Errorf("max entries must be positive: %d", v)
	}

	return nil
}

func validateHistoricalEntries(i interface{}) error {
	_, ok := i.(uint32)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	return nil
}

func validateBondDenom(i interface{}) error {
	v, ok := i.(string)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if strings.TrimSpace(v) == "" {
		return errors.New("bond denom cannot be blank")
	}

	if err := sdk.ValidateDenom(v); err != nil {
		return err
	}

	return nil
}

func ValidatePowerReduction(i interface{}) error {
	v, ok := i.(math.Int)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v.LT(math.NewInt(1)) {
		return fmt.Errorf("power reduction cannot be lower than 1")
	}

	return nil
}

func validateMinCommissionRate(i interface{}) error {
	v, ok := i.(math.LegacyDec)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v.IsNil() {
		return fmt.Errorf("minimum commission rate cannot be nil: %s", v)
	}
	if v.IsNegative() {
		return fmt.Errorf("minimum commission rate cannot be negative: %s", v)
	}
	if v.GT(math.LegacyOneDec()) {
		return fmt.Errorf("minimum commission rate cannot be greater than 100%%: %s", v)
	}

	return nil
}

// Validation functions for new parameters
func validateStakeWeightFactor(i interface{}) error {
	v, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v > 100 {
		return fmt.Errorf("stake weight factor must be between 0 and 100: %d", v)
	}

	return nil
}

func validateBaselineChanceFactor(i interface{}) error {
	v, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v > 100 {
		return fmt.Errorf("baseline chance factor must be between 0 and 100: %d", v)
	}

	return nil
}

func validateRandomnessFactor(i interface{}) error {
	v, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v > 100 {
		return fmt.Errorf("randomness factor must be between 0 and 100: %d", v)
	}

	return nil
}
