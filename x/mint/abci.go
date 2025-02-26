package mint

import (
	"context"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/mint/keeper"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

// BeginBlocker mints new tokens at the start of each block.
func BeginBlocker(ctx context.Context, k keeper.Keeper) error {
	defer telemetry.ModuleMeasureSince(types.ModuleName, telemetry.Now(), telemetry.MetricKeyBeginBlocker)

	// Fetch stored minter & params
	minter, err := k.Minter.Get(ctx)
	if err != nil {
		return err
	}

	// Get total HELIOS supply
	totalSupply, err := k.TotalHeliosSupply(ctx)
	if err != nil {
		return err
	}

	// Define the inflation rate based on the supply phase - using governance-defined rates
	var inflationRate math.LegacyDec
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	switch {

	case totalSupply.LT(types.HeliosToBaseUnits(types.EarlyStageThreshold)):
		inflationRate, err = k.GetEarlyPhaseInflationRate(ctx)
		if err != nil {
			sdkCtx.Logger().Error("failed to get early phase inflation rate", "error", err)
			inflationRate = math.LegacyNewDecWithPrec(15, 2) // Default 15%
		}
	case totalSupply.LT(types.HeliosToBaseUnits(types.GrowthStageThreshold)):
		inflationRate, err = k.GetGrowthPhaseInflationRate(ctx)
		if err != nil {
			sdkCtx.Logger().Error("failed to get growth phase inflation rate", "error", err)
			inflationRate = math.LegacyNewDecWithPrec(12, 2) // Default 12%
		}
	case totalSupply.LT(types.HeliosToBaseUnits(types.MatureStageThreshold)):
		inflationRate, err = k.GetMaturePhaseInflationRate(ctx)
		if err != nil {
			sdkCtx.Logger().Error("failed to get mature phase inflation rate", "error", err)
			inflationRate = math.LegacyNewDecWithPrec(5, 2) // Default 5%
		}
	default: // Post-Cap Phase (>5B HELIOS)
		inflationRate, err = k.GetPostCapInflationRate(ctx)
		if err != nil {
			sdkCtx.Logger().Error("failed to get post-cap inflation rate", "error", err)
			inflationRate = math.LegacyNewDecWithPrec(3, 2) // Default 3%
		}
	}

	// Calculate annual provisions based on the inflation rate and total supply
	annualProvisions := math.LegacyNewDecFromInt(totalSupply).Mul(inflationRate)

	// Calculate the mint amount per block
	blocksPerYear := math.LegacyNewDec(int64(60 * 60 * 24 * 365 / 5)) // 5s block time
	mintPerBlock := annualProvisions.Quo(blocksPerYear)

	// Stop minting if inflation rate is zero
	if inflationRate.IsZero() {
		return nil // No minting when inflation is set to zero
	}

	// Get helios bond denom
	bondDenom, err := k.HeliosDenom(ctx)
	if err != nil {
		return err
	}

	// Create coins to mint
	mintedCoins := sdk.NewCoins(sdk.NewCoin(bondDenom, mintPerBlock.TruncateInt()))

	// Mint new HELIOS tokens
	err = k.MintCoins(ctx, mintedCoins)
	if err != nil {
		return err
	}

	// Send the minted coins to the fee collector for distribution
	err = k.AddCollectedFees(ctx, mintedCoins)
	if err != nil {
		return err
	}

	// Store the new inflation rate and annual provisions
	minter.Inflation = inflationRate
	minter.AnnualProvisions = annualProvisions
	if err = k.Minter.Set(ctx, minter); err != nil {
		return err
	}

	// Emit minting event with phase information
	var phaseInfo string
	switch {
	case totalSupply.LT(types.HeliosToBaseUnits(types.EarlyStageThreshold)):
		phaseInfo = "early"
	case totalSupply.LT(types.HeliosToBaseUnits(types.GrowthStageThreshold)):
		phaseInfo = "growth"
	case totalSupply.LT(types.HeliosToBaseUnits(types.MatureStageThreshold)):
		phaseInfo = "mature"
	default:
		phaseInfo = "post-cap"
	}

	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeMint,
			sdk.NewAttribute(types.AttributeKeyInflation, inflationRate.String()),
			sdk.NewAttribute(types.AttributeKeyAnnualProvisions, minter.AnnualProvisions.String()),
			sdk.NewAttribute(sdk.AttributeKeyAmount, mintPerBlock.TruncateInt().String()),
			sdk.NewAttribute("phase", phaseInfo),
			sdk.NewAttribute("total_supply", totalSupply.String()),
		),
	)

	return nil
}
