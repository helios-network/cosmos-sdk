package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

func (k Keeper) HandleUpdateInflationRateProposal(ctx sdk.Context, content govtypes.Content) error {
	switch c := content.(type) {
	case *types.UpdateInflationProposal:
		return k.HandleUpdateInflationProposal(ctx, c)
	default:
		return fmt.Errorf("unrecognized proposal content type: %T", c)
	}
}

// HandleUpdateInflationProposal processes governance-approved inflation rate updates
func (k Keeper) HandleUpdateInflationProposal(ctx context.Context, p *types.UpdateInflationProposal) error {
	// Validate phase
	validPhases := map[string]bool{
		"early":    true,
		"growth":   true,
		"mature":   true,
		"post-cap": true,
	}
	if !validPhases[p.Phase] {
		return fmt.Errorf("invalid phase: %s, must be one of: early, growth, mature, post-cap", p.Phase)
	}

	// Validate rate is between 0 and 100%
	if p.NewRate.IsNegative() || p.NewRate.GT(math.LegacyNewDecWithPrec(100, 2)) {
		return fmt.Errorf("inflation rate must be between 0%% and 100%%")
	}

	// Get current rate to check for maximum change per proposal
	var currentRate math.LegacyDec
	var err error

	switch p.Phase {
	case "early":
		currentRate, err = k.GetEarlyPhaseInflationRate(ctx)
	case "growth":
		currentRate, err = k.GetGrowthPhaseInflationRate(ctx)
	case "mature":
		currentRate, err = k.GetMaturePhaseInflationRate(ctx)
	case "post-cap":
		currentRate, err = k.GetPostCapInflationRate(ctx)
	}

	if err != nil {
		return fmt.Errorf("failed to get current inflation rate: %w", err)
	}

	// Maximum allowed change per proposal (3%)
	maxChangePerProposal := math.LegacyNewDecWithPrec(3, 2)

	// Calculate absolute difference between current and proposed rates
	rateDifference := p.NewRate.Sub(currentRate)
	absoluteDifference := rateDifference
	if absoluteDifference.IsNegative() {
		absoluteDifference = absoluteDifference.Neg()
	}

	// Check if the proposed change exceeds the maximum allowed change
	if absoluteDifference.GT(maxChangePerProposal) {
		return fmt.Errorf(
			"proposed change of %s exceeds maximum allowed change of %s per proposal (current: %s, proposed: %s)",
			absoluteDifference.String(),
			maxChangePerProposal.String(),
			currentRate.String(),
			p.NewRate.String(),
		)
	}

	// Determine which phase inflation rate to update
	switch p.Phase {
	case "early":
		err = k.SetEarlyPhaseInflationRate(ctx, p.NewRate)
	case "growth":
		err = k.SetGrowthPhaseInflationRate(ctx, p.NewRate)
	case "mature":
		err = k.SetMaturePhaseInflationRate(ctx, p.NewRate)
	case "post-cap":
		err = k.SetPostCapInflationRate(ctx, p.NewRate)
	}

	if err != nil {
		return fmt.Errorf("failed to update inflation rate: %w", err)
	}

	// Log success with detailed information
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeInflationRateUpdate,
			sdk.NewAttribute(types.AttributeKeyPhase, p.Phase),
			sdk.NewAttribute(types.AttributeKeyNewRate, p.NewRate.String()),
			sdk.NewAttribute("previous_rate", currentRate.String()),
			sdk.NewAttribute("rate_change", rateDifference.String()),
		),
	)

	// Direction of change for logging
	changeDirection := "increased by"
	if rateDifference.IsNegative() {
		changeDirection = "decreased by"
		rateDifference = rateDifference.Neg() // Make positive for display
	}

	sdkCtx.Logger().Info(
		fmt.Sprintf("Inflation rate for %s phase updated from %s to %s (%s %s)",
			p.Phase,
			currentRate.String(),
			p.NewRate.String(),
			changeDirection,
			rateDifference.String(),
		),
	)

	return nil
}
