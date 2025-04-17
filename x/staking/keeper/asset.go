// AddOrUpdateAssetWeight adds a new asset weight or updates an existing one in the delegation's asset weights
package keeper

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

func (k Keeper) GetTreasuryAddress(ctx sdk.Context) (sdk.AccAddress, error) {
	// Get the genesis validator address that's being used as treasury
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get params: %w", err)
	}
	if params.TreasuryAddress == "" {
		return nil, fmt.Errorf("treasury address not set in genesis")
	}

	treasuryAddr, err := sdk.AccAddressFromBech32(params.TreasuryAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid treasury address in genesis: %w", err)
	}

	// Verify the account exists
	acc := k.authKeeper.GetAccount(ctx, treasuryAddr)
	if acc == nil {
		return nil, fmt.Errorf("treasury account does not exist: %s", params.TreasuryAddress)
	}

	k.Logger(ctx).Info("Fetched Treasury Address from Genesis", "address", treasuryAddr.String())

	return treasuryAddr, nil
}

// collectSlashedAssets directly transfers slashed assets to the Treasury
func (k Keeper) collectSlashedAssets(ctx sdk.Context, validatorAddr string, realSlashedAssets map[string]sdk.Coin) error {
	// Define Treasury (Genesis) wallet address
	treasuryAddr, err := k.GetTreasuryAddress(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch treasury address: %w", err)
	}

	var coinsToTransfer sdk.Coins

	// Iterate through slashed assets and prepare transfer
	for _, realAsset := range realSlashedAssets {
		coinsToTransfer = coinsToTransfer.Add(realAsset)
	}

	// Send slashed coins to the treasury
	if !coinsToTransfer.Empty() {
		err := k.bankKeeper.SafeTransferTreasury(ctx, treasuryAddr, coinsToTransfer)
		if err != nil {
			k.Logger(ctx).Error("Failed to send slashed assets to treasury", "amount", coinsToTransfer.String(), "error", err)
			return err
		}
	}

	k.Logger(ctx).Info("Slashed assets successfully collected and sent to treasury", "validator", validatorAddr, "assets", coinsToTransfer.String())

	return nil
}

func (k Keeper) AddOrUpdateAssetWeight(
	delegation *types.Delegation,
	asset sdk.Coin, // coin => bondenom : ahelios and amt = weigted
	bondDenom string, // erc20 or asset bondDenom
	baseAmount math.Int,
) (*types.Delegation, error) {

	// Validate inputs
	if delegation == nil {
		return nil, fmt.Errorf("delegation cannot be nil")
	}

	if asset.IsZero() {
		return nil, fmt.Errorf("asset amount cannot be zero")
	}

	if baseAmount.IsNegative() {
		return nil, fmt.Errorf("base amount cannot be negative")
	}

	targetDenom := bondDenom

	assetWeightIndex := delegation.FindAssetWeightIndex(targetDenom)

	if assetWeightIndex == -1 {
		delegation.AssetWeights = append(delegation.AssetWeights, &types.AssetWeight{
			Denom:          targetDenom,
			BaseAmount:     baseAmount,
			WeightedAmount: asset.Amount,
		})
	} else {
		delegation.AssetWeights[assetWeightIndex].BaseAmount = delegation.AssetWeights[assetWeightIndex].BaseAmount.Add(baseAmount)
		delegation.AssetWeights[assetWeightIndex].WeightedAmount = delegation.AssetWeights[assetWeightIndex].WeightedAmount.Add(asset.Amount)
	}
	// Recalculate total weighted amount
	delegation.TotalWeightedAmount = math.ZeroInt()
	for _, aw := range delegation.AssetWeights {
		delegation.TotalWeightedAmount = delegation.TotalWeightedAmount.Add(aw.WeightedAmount)
	}

	return delegation, nil
}

// ConvertWeightedToRealAsset converts a weighted amount back to the actual asset amount.
func (k Keeper) ConvertWeightedToRealAsset(ctx sdk.Context, denom string, weightedAmount math.Int) (sdk.Coin, error) {
	if weightedAmount.IsNegative() {
		return sdk.Coin{}, fmt.Errorf("invalid weighted amount received")
	}

	// Get all staking assets
	stakingAssets := k.erc20Keeper.GetAllStakingAssets(ctx)

	// Find the matching asset weight
	for _, asset := range stakingAssets {
		if asset.GetDenom() == denom {
			weight := math.NewIntFromUint64(asset.GetBaseWeight())
			if weight.IsZero() {
				return sdk.Coin{}, fmt.Errorf("weight for %s is zero, cannot convert", denom)
			}

			// Convert weighted amount back to real asset value
			realAmount := weightedAmount.Quo(weight)

			// Return Cosmos SDK coin with original denom
			return sdk.NewCoin(denom, realAmount), nil
		}
	}

	return sdk.Coin{}, fmt.Errorf("denom %s not found in staking assets", denom)
}

func (k Keeper) ConvertAssetToSDKCoin(ctx sdk.Context, denom string, amount math.Int) (sdk.Coin, error) {
	if amount.IsNegative() {
		return sdk.Coin{}, fmt.Errorf("invalid amount received in delegation")
	}
	// Get all staking assets
	stakingAssets := k.erc20Keeper.GetAllStakingAssets(ctx)

	baseDenom, err := sdk.GetBaseDenom()
	if err != nil {
		return sdk.Coin{}, fmt.Errorf("error while retreive base denom")
	}

	// Find the matching asset
	for _, asset := range stakingAssets {
		if asset.GetDenom() == denom {
			// Apply weight conversion
			weight := math.NewIntFromUint64(asset.GetBaseWeight())
			weightedAmount := amount.Mul(weight)
			// Return Cosmos SDK coin
			return sdk.NewCoin(baseDenom, weightedAmount), nil
		}
	}

	if denom == baseDenom { // we're receiving base ahelios from genesis or from an internal call
		return sdk.NewCoin(denom, amount), nil
	}

	return sdk.Coin{}, fmt.Errorf("denom %s not found in staking assets: %s", denom, baseDenom)
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

	assetWeightIndex := delegation.FindAssetWeightIndex(denom)

	if assetWeightIndex == -1 {
		return fmt.Errorf("insufficient balance %s", denom)
	}

	assetWeight := delegation.AssetWeights[assetWeightIndex]

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
		delegation.AssetWeights = append(delegation.AssetWeights[:assetWeightIndex], delegation.AssetWeights[assetWeightIndex+1:]...)
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

		delegation.AssetWeights[assetWeightIndex] = assetWeight
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
			assetWeightIndex := delegation.FindAssetWeightIndex(denom)
			if assetWeightIndex != -1 {
				assetWeight := delegation.AssetWeights[assetWeightIndex]
				oldWeightedAmount := assetWeight.WeightedAmount

				// Calculate adjustment
				adjustmentAmount := math.LegacyNewDecFromInt(assetWeight.WeightedAmount).Mul(percentage)
				if increase {
					assetWeight.WeightedAmount = assetWeight.WeightedAmount.Add(adjustmentAmount.TruncateInt())
				} else {
					assetWeight.WeightedAmount = assetWeight.WeightedAmount.Sub(adjustmentAmount.TruncateInt())
				}

				// Update the asset weight
				delegation.AssetWeights[assetWeightIndex] = assetWeight

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
				// Save updated delegation
				delegations[i] = delegation
				k.SetDelegation(ctx, delegation)
				// Update startDelegationInfo
				if err := k.Hooks().AfterDelegationModified(ctx, delAddr, sdk.ValAddress(valAddr)); err != nil {
					return err
				}
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
