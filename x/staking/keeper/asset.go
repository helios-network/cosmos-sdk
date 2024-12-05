// AddOrUpdateAssetWeight adds a new asset weight or updates an existing one in the delegation's asset weights
package keeper

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

func (k Keeper) AddOrUpdateAssetWeight(
	delegation *types.Delegation,
	asset sdk.Coin,
	baseAmount math.Int,
) error {
	// Validate inputs
	if delegation == nil {
		return fmt.Errorf("delegation cannot be nil")
	}

	if asset.IsZero() {
		return fmt.Errorf("asset amount cannot be zero")
	}

	if baseAmount.IsNegative() {
		return fmt.Errorf("base amount cannot be negative")
	}

	// Initialize AssetWeights map if not exists
	if delegation.AssetWeights == nil {
		delegation.AssetWeights = make(map[string]*types.AssetWeight)
	}

	// Get or create asset weight for the denom
	assetWeight, exists := delegation.AssetWeights[asset.Denom]
	if !exists {
		// Create new asset weight
		assetWeight = &types.AssetWeight{
			Denom:          asset.Denom,
			BaseAmount:     baseAmount,
			WeightedAmount: asset.Amount,
		}
		delegation.AssetWeights[asset.Denom] = assetWeight
	} else {
		// Update existing asset weight
		assetWeight.BaseAmount = assetWeight.BaseAmount.Add(baseAmount)
		assetWeight.WeightedAmount = assetWeight.WeightedAmount.Add(asset.Amount)
	}

	// Recalculate total weighted amount
	delegation.TotalWeightedAmount = math.ZeroInt()
	for _, aw := range delegation.AssetWeights {
		delegation.TotalWeightedAmount = delegation.TotalWeightedAmount.Add(aw.WeightedAmount)
	}

	return nil
}

func (k Keeper) ConvertAssetToSDKCoin(ctx sdk.Context, denom string, amount math.Int) (sdk.Coin, error) {
	if amount.IsNegative() {
		return sdk.Coin{}, fmt.Errorf("invalid amount received in delegation")
	}
	// Get all staking assets
	stakingAssets := k.erc20Keeper.GetAllStakingAssets(ctx)

	// Find the matching asset
	for _, asset := range stakingAssets {
		if asset.GetDenom() == denom {
			// Apply weight conversion
			weight := math.NewIntFromUint64(asset.GetBaseWeight())
			weightedAmount := amount.Mul(weight).Quo(math.NewInt(100))

			// Return Cosmos SDK coin
			return sdk.NewCoin(denom, weightedAmount), nil
		}
	}

	return sdk.Coin{}, fmt.Errorf("denom %s not found in staking assets", denom)
}

func (k Keeper) UpdateOrRemoveAssetWeight(
	delegation *types.Delegation,
	denom string,
	amountToRemove math.Int,
	ctx sdk.Context,
) error {
	if delegation == nil {
		return fmt.Errorf("delegation cannot be nil")
	}

	if delegation.AssetWeights == nil {
		return fmt.Errorf("delegation has no asset weights")
	}

	assetWeight, exists := delegation.AssetWeights[denom]
	if !exists {
		return fmt.Errorf("asset weight for denom %s does not exist", denom)
	}

	if amountToRemove.IsNegative() {
		return fmt.Errorf("amount to remove cannot be negative")
	}

	// Calculate new weighted amount
	newWeightedAmount := assetWeight.WeightedAmount.Sub(amountToRemove)

	if newWeightedAmount.IsNegative() {
		return fmt.Errorf("cannot remove more than existing weighted amount")
	}

	if newWeightedAmount.IsZero() {
		// Remove the entire asset weight if no amount remains
		delete(delegation.AssetWeights, denom)
	} else {
		// Update the asset weight with reduced amount
		assetWeight.WeightedAmount = newWeightedAmount

		// Get the weight factor for this asset
		stakingAssets := k.erc20Keeper.GetAllStakingAssets(ctx)
		var weightFactor math.Int
		for _, asset := range stakingAssets {
			if asset.GetDenom() == denom {
				weightFactor = math.NewIntFromUint64(asset.GetBaseWeight())
				break
			}
		}

		// Calculate base amount to remove
		baseAmountToRemove := amountToRemove.Quo(weightFactor)
		assetWeight.BaseAmount = assetWeight.BaseAmount.Sub(baseAmountToRemove)

		delegation.AssetWeights[denom] = assetWeight
	}

	// Recalculate total weighted amount
	delegation.TotalWeightedAmount = math.ZeroInt()
	for _, aw := range delegation.AssetWeights {
		delegation.TotalWeightedAmount = delegation.TotalWeightedAmount.Add(aw.WeightedAmount)
	}

	return nil
}

// When asset weight is updated through governance
func (k Keeper) UpdateAssetWeight(ctx sdk.Context, denom string, percentage math.LegacyDec, increase bool) error {
	// Pre-fetch staking asset weights
	stakingAssets := k.erc20Keeper.GetAllStakingAssets(ctx)
	assetWeightMap := make(map[string]math.Int)
	for _, asset := range stakingAssets {
		assetWeightMap[asset.GetDenom()] = math.NewIntFromUint64(asset.GetBaseWeight())
	}

	// Ensure the denom exists in the staking assets
	if _, exists := assetWeightMap[denom]; !exists {
		return fmt.Errorf("denom %s not found in staking assets", denom)
	}

	if percentage.IsZero() {
		return nil // No need to process if percentage is zero
	}

	// Iterate over validators
	validators, err := k.GetAllValidators(ctx)
	if err != nil {
		return fmt.Errorf("failed to get validators: %w", err)
	}

	for _, validator := range validators {
		valAddr, err := k.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
		if err != nil {
			return fmt.Errorf("failed to decode validator address: %w", err)
		}

		delegations, err := k.GetValidatorDelegations(ctx, sdk.ValAddress(valAddr))
		if err != nil {
			return fmt.Errorf("failed to get delegations for validator: %w", err)
		}

		// Batch updates for validator tokens and shares
		totalValidatorDiff := math.ZeroInt()

		for i, delegation := range delegations {
			if assetWeight, exists := delegation.AssetWeights[denom]; exists {
				oldWeightedAmount := assetWeight.WeightedAmount

				// Calculate adjustment
				adjustmentAmount := math.LegacyNewDecFromInt(assetWeight.WeightedAmount).Mul(percentage)
				if increase {
					assetWeight.WeightedAmount = assetWeight.WeightedAmount.Add(adjustmentAmount.TruncateInt())
				} else {
					assetWeight.WeightedAmount = assetWeight.WeightedAmount.Sub(adjustmentAmount.TruncateInt())
				}

				// Update the asset weight
				delegation.AssetWeights[denom] = assetWeight

				// Calculate total difference for this delegation
				totalDiff := assetWeight.WeightedAmount.Sub(oldWeightedAmount)
				totalValidatorDiff = totalValidatorDiff.Add(totalDiff)

				delegation.TotalWeightedAmount = delegation.TotalWeightedAmount.Add(totalDiff)

				delAddr, err := sdk.AccAddressFromBech32(delegation.DelegatorAddress)
				if err != nil {
					return fmt.Errorf("failed to decode delegator address: %w", err)
				}

				//TODO: Remove hooks calls if we don't want auto distrib of rewards on each updates (which may lead to extra useless comsuption)
				if err := k.Hooks().BeforeDelegationSharesModified(ctx, delAddr, sdk.ValAddress(valAddr)); err != nil {
					return err
				}

				// Update delegation shares
				delegation.Shares = delegation.Shares.Add(math.LegacyNewDecFromInt(totalDiff))
				if err := k.Hooks().AfterDelegationModified(ctx, delAddr, sdk.ValAddress(valAddr)); err != nil {
					return err
				}

				// Save updated delegation
				delegations[i] = delegation
				k.SetDelegation(ctx, delegation)
			}
		}

		// Apply total validator updates
		if totalValidatorDiff.IsPositive() {
			validator, _, err = k.AddValidatorTokensAndShares(ctx, validator, totalValidatorDiff)
		} else if totalValidatorDiff.IsNegative() {
			validator, _, err = k.RemoveValidatorTokensAndShares(ctx, validator, math.LegacyNewDecFromInt(totalValidatorDiff.Abs()))
		}
		if err != nil {
			return fmt.Errorf("failed to update validator tokens and shares: %w", err)
		}

		// Save updated validator
		k.SetValidator(ctx, validator)
	}

	return nil
}
