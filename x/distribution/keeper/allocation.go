package keeper

import (
	"context"

	abci "github.com/cometbft/cometbft/abci/types"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// AllocateTokens performs reward and fee distribution to all validators based
// on the F1 fee distribution specification.
// AllocateTokens performs reward and fee distribution to all validators based
// on the F1 fee distribution specification with stake reduction for large delegations.
func (k Keeper) AllocateTokens(ctx context.Context, totalPreviousPower int64, bondedVotes []abci.VoteInfo) error {
	// fetch and clear the collected fees for distribution
	feeCollector := k.authKeeper.GetModuleAccount(ctx, k.feeCollectorName)
	feesCollectedInt := k.bankKeeper.GetAllBalances(ctx, feeCollector.GetAddress())
	feesCollected := sdk.NewDecCoinsFromCoins(feesCollectedInt...)

	// transfer collected fees to the distribution module account
	err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, k.feeCollectorName, types.ModuleName, feesCollectedInt)
	if err != nil {
		return err
	}

	// temporary workaround to keep CanWithdrawInvariant happy
	feePool, err := k.FeePool.Get(ctx)
	if err != nil {
		return err
	}

	if totalPreviousPower == 0 {
		feePool.CommunityPool = feePool.CommunityPool.Add(feesCollected...)
		return k.FeePool.Set(ctx, feePool)
	}

	// calculate fraction allocated to validators
	remaining := feesCollected
	communityTax, err := k.GetCommunityTax(ctx)
	if err != nil {
		return err
	}

	voteMultiplier := math.LegacyOneDec().Sub(communityTax)
	feeMultiplier := feesCollected.MulDecTruncate(voteMultiplier)

	// Check if stake reduction is enabled
	stakingParams, err := k.stakingKeeper.GetParams(ctx)
	if err != nil {
		return err
	}

	isStakeReductionEnabled := stakingParams.DelegatorStakeReduction != nil && stakingParams.DelegatorStakeReduction.Enabled

	// Get total staked tokens for calculations if needed
	var totalNetworkStake math.Int
	if isStakeReductionEnabled {
		totalNetworkStake, err = k.stakingKeeper.TotalBondedTokens(ctx)
		if err != nil {
			return err
		}
	}

	// Single pass implementation that works for both cases
	// We'll store validator data and accumulate effective total power
	type validatorData struct {
		validator      stakingtypes.ValidatorI
		effectivePower math.LegacyDec
	}

	// Prepare storage for validator data
	validatorsData := make([]validatorData, 0, len(bondedVotes))
	effectiveTotalPower := math.LegacyZeroDec()

	// First part of the single pass: calculate effective powers and accumulate total
	for _, vote := range bondedVotes {
		validator, err := k.stakingKeeper.ValidatorByConsAddr(ctx, vote.Validator.Address)
		if err != nil {
			return err
		}

		var effectivePower math.LegacyDec
		if isStakeReductionEnabled {
			// Calculate effective power with stake reduction
			effectivePower, err = k.calculateEffectivePower(ctx, validator, totalNetworkStake)
			if err != nil {
				return err
			}
		} else {
			// Use original power when stake reduction is disabled
			effectivePower = math.LegacyNewDec(vote.Validator.Power)
		}

		// Store validator data for the second part
		validatorsData = append(validatorsData, validatorData{
			validator:      validator,
			effectivePower: effectivePower,
		})

		// Accumulate total effective power
		effectiveTotalPower = effectiveTotalPower.Add(effectivePower)
	}

	// Second part of the single pass: allocate rewards based on effective power fractions
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for _, valData := range validatorsData {
		// Calculate power fraction using effective power
		powerFraction := valData.effectivePower.QuoTruncate(effectiveTotalPower)
		reward := feeMultiplier.MulDecTruncate(powerFraction)

		if isStakeReductionEnabled {
			// Log allocation details for debugging when stake reduction is enabled
			sdkCtx.Logger().Debug(
				"allocating validator rewards with stake reduction",
				"validator", valData.validator.GetOperator(),
				"effective_power", valData.effectivePower,
				"power_fraction", powerFraction,
				"reward", reward,
			)
		}

		err = k.AllocateTokensToValidator(ctx, valData.validator, reward)
		if err != nil {
			return err
		}

		remaining = remaining.Sub(reward)
	}

	// allocate community funding
	feePool.CommunityPool = feePool.CommunityPool.Add(remaining...)
	return k.FeePool.Set(ctx, feePool)
}

// AllocateTokensToValidator allocate tokens to a particular validator,
// splitting according to commission.
func (k Keeper) AllocateTokensToValidator(ctx context.Context, val stakingtypes.ValidatorI, tokens sdk.DecCoins) error {
	// split tokens between validator and delegators according to commission
	commission := tokens.MulDec(val.GetCommission())
	shared := tokens.Sub(commission)

	valBz, err := k.stakingKeeper.ValidatorAddressCodec().StringToBytes(val.GetOperator())
	if err != nil {
		return err
	}

	// update current commission
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeCommission,
			sdk.NewAttribute(sdk.AttributeKeyAmount, commission.String()),
			sdk.NewAttribute(types.AttributeKeyValidator, val.GetOperator()),
		),
	)
	currentCommission, err := k.GetValidatorAccumulatedCommission(ctx, valBz)
	if err != nil {
		return err
	}

	currentCommission.Commission = currentCommission.Commission.Add(commission...)
	err = k.SetValidatorAccumulatedCommission(ctx, valBz, currentCommission)
	if err != nil {
		return err
	}

	// update current rewards
	currentRewards, err := k.GetValidatorCurrentRewards(ctx, valBz)
	if err != nil {
		return err
	}

	currentRewards.Rewards = currentRewards.Rewards.Add(shared...)
	err = k.SetValidatorCurrentRewards(ctx, valBz, currentRewards)
	if err != nil {
		return err
	}

	// update outstanding rewards
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeRewards,
			sdk.NewAttribute(sdk.AttributeKeyAmount, tokens.String()),
			sdk.NewAttribute(types.AttributeKeyValidator, val.GetOperator()),
		),
	)

	outstanding, err := k.GetValidatorOutstandingRewards(ctx, valBz)
	if err != nil {
		return err
	}

	outstanding.Rewards = outstanding.Rewards.Add(tokens...)
	return k.SetValidatorOutstandingRewards(ctx, valBz, outstanding)
}

// expNeg calculates e^(-x) for the LegacyDec type
func expNeg(x math.LegacyDec) math.LegacyDec {
	result := math.LegacyOneDec()
	term := math.LegacyOneDec()
	negX := x.Neg()

	for i := 1; i <= 10; i++ {
		term = term.Mul(negX).Quo(math.LegacyNewDec(int64(i))) // Directly divide by i to avoid large factorial
		result = result.Add(term)
	}

	return result
}

// calculateEffectivePower applies stake reduction only to large delegations
func (k Keeper) calculateEffectivePower(ctx context.Context, validator stakingtypes.ValidatorI, totalNetworkStake math.Int) (math.LegacyDec, error) {
	// Get validator address
	valAddrStr := validator.GetOperator()
	valAddr, err := k.stakingKeeper.ValidatorAddressCodec().StringToBytes(valAddrStr)
	if err != nil {
		return math.LegacyZeroDec(), err
	}

	// Get all delegations to this validator
	delegations, err := k.stakingKeeper.GetValidatorDelegations(ctx, valAddr)
	if err != nil {
		return math.LegacyZeroDec(), err
	}

	// Check if stake reduction is enabled
	stakingParams, err := k.stakingKeeper.GetParams(ctx)
	if err != nil {
		return math.LegacyZeroDec(), err
	}

	// If stake reduction is not enabled, return original power
	if stakingParams.DelegatorStakeReduction == nil || !stakingParams.DelegatorStakeReduction.Enabled {
		return math.LegacyNewDecFromInt(validator.GetTokens()), nil
	}

	// Calculate effective stake for each delegation
	totalEffectiveStake := math.LegacyZeroDec()

	for _, delegation := range delegations {
		// Use validator's built-in TokensFromShares method
		delTokens := validator.TokensFromShares(delegation.GetShares())

		// Create adjusted total network stake by subtracting this delegation's stake
		adjustedNetworkStake := totalNetworkStake.Sub(delTokens.TruncateInt())

		// Use adjusted threshold that excludes this delegation from total
		thresholdPercentage := stakingParams.DelegatorStakeReduction.DominanceThreshold
		networkThreshold := math.LegacyNewDecFromInt(adjustedNetworkStake).Mul(thresholdPercentage)

		// Check if this delegation exceeds the adjusted network threshold
		if delTokens.GT(networkThreshold) {
			// Calculate excess amount above threshold
			excess := delTokens.Sub(networkThreshold)

			// Calculate how far above threshold is this delegation (as % of adjusted total network stake)
			excessPercentage := excess.Quo(math.LegacyNewDecFromInt(adjustedNetworkStake))

			// Calculate reduction factor based on curve
			steepnessParam := excessPercentage.Mul(stakingParams.DelegatorStakeReduction.CurveSteepness).Neg()
			oneMinusExp := math.LegacyOneDec().Sub(expNeg(steepnessParam))

			reductionFactor := stakingParams.DelegatorStakeReduction.MaxReduction.Mul(oneMinusExp)

			// Apply reduction to excess only
			reducedExcess := excess.Mul(math.LegacyOneDec().Sub(reductionFactor))
			effectiveStake := networkThreshold.Add(reducedExcess)

			// Log reduction for debugging
			sdkCtx := sdk.UnwrapSDKContext(ctx)
			sdkCtx.Logger().Debug(
				"applied stake reduction to large delegation",
				"validator", valAddrStr,
				"delegator", delegation.GetDelegatorAddr(),
				"original_stake", delTokens,
				"effective_stake", effectiveStake,
				"adjusted_network_stake", adjustedNetworkStake,
				"threshold", networkThreshold,
				"reduction_factor", reductionFactor,
			)

			totalEffectiveStake = totalEffectiveStake.Add(effectiveStake)
		} else {
			// No reduction for small delegations
			totalEffectiveStake = totalEffectiveStake.Add(delTokens)
		}
	}

	return totalEffectiveStake, nil
}
