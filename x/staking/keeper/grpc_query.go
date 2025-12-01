package keeper

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/math"
	"cosmossdk.io/store/prefix"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Querier is used as Keeper will have duplicate methods if used directly, and gRPC names take precedence over keeper
type Querier struct {
	*Keeper
}

var _ types.QueryServer = Querier{}

func NewQuerier(keeper *Keeper) Querier {
	return Querier{Keeper: keeper}
}

// Validators queries all validators that match the given status
func (k Querier) Validators(ctx context.Context, req *types.QueryValidatorsRequest) (*types.QueryValidatorsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	// Validate the provided status, return all the validators if the status is empty
	if req.Status != "" && !(req.Status == types.Bonded.String() || req.Status == types.Unbonded.String() || req.Status == types.Unbonding.String()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator status %s", req.Status)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	store := k.Keeper.storeService.OpenKVStore(sdkCtx)
	valStore := prefix.NewStore(runtime.KVStoreAdapter(store), types.ValidatorsKey)

	var allValidators []types.Validator
	var pageRes *query.PageResponse

	if req.Pagination == nil {
		return nil, status.Error(codes.InvalidArgument, "pagination is required")
	}
	if req.Pagination.CountTotal {
		req.Pagination.CountTotal = false
	}

	if req.Status != "" {
		// If a specific status is requested, use GenericFilteredPaginate directly
		queriedValidators, pRes, err := query.GenericFilteredPaginate(k.cdc, valStore, req.Pagination, func(key []byte, val *types.Validator) (*types.Validator, error) {
			if !strings.EqualFold(val.GetStatus().String(), req.Status) {
				return nil, nil
			}
			return val, nil
		}, func() *types.Validator {
			return &types.Validator{}
		})
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		for _, val := range queriedValidators {
			allValidators = append(allValidators, *val)
		}
		pageRes = pRes
	} else {
		iter := valStore.Iterator(nil, nil)
		defer iter.Close()

		var bonded, unbonding, unbonded, heliosBetaMainnet []types.Validator

		for ; iter.Valid(); iter.Next() {
			var v types.Validator
			if err := k.cdc.Unmarshal(iter.Value(), &v); err != nil {
				return nil, err
			}

			valAddr, _ := sdk.ValAddressFromBech32(v.OperatorAddress)
			validatorCosmosAddress := sdk.AccAddress(valAddr.Bytes())

			if slices.Contains(types.HeliosBetaMainnetWallets, validatorCosmosAddress.String()) {
				heliosBetaMainnet = append(heliosBetaMainnet, v)
				continue
			}

			switch v.GetStatus() {
			case types.Bonded:
				bonded = append(bonded, v)
			case types.Unbonding:
				unbonding = append(unbonding, v)
			case types.Unbonded:
				unbonded = append(unbonded, v)
			}

			// EARLY STOP POSSIBLE
			if uint64(len(bonded)) >= req.Pagination.Limit {
				break
			}
		}

		allValidators = append(append(append(heliosBetaMainnet, bonded...), unbonding...), unbonded...)
		// slice the allValidators to the limit
		if len(allValidators) > int(req.Pagination.Offset+req.Pagination.Limit) {
			allValidators = allValidators[req.Pagination.Offset : req.Pagination.Offset+req.Pagination.Limit]
		} else if len(allValidators) > int(req.Pagination.Offset) {
			allValidators = allValidators[req.Pagination.Offset:]
		} else {
			allValidators = []types.Validator{}
		}
		// Construct PageResponse
		pageRes = &query.PageResponse{Total: 0}
	}

	return &types.QueryValidatorsResponse{Validators: allValidators, Pagination: pageRes}, nil
}

func (k Querier) ValidatorsCount(ctx context.Context, req *types.QueryValidatorsCountRequest) (*types.QueryValidatorsCountResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	count := uint64(0)
	err := k.IterateValidators(ctx, func(index int64, validator types.ValidatorI) (stop bool) {
		if req.Status != "" && !strings.EqualFold(validator.GetStatus().String(), req.Status) {
			return false
		}
		count++
		return false
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryValidatorsCountResponse{Count: count}, nil
}

func (k Querier) ShareRepartitionMap(ctx context.Context, req *types.QueryShareRepartitionMapRequest) (*types.QueryShareRepartitionMapResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := sdkCtx.BlockHeight()

	// Try to get historical data with pre-calculated TotalAssetWeights
	var historicalInfo types.HistoricalInfo
	var found bool

	for height := currentHeight; height >= currentHeight-2; height-- {
		if hi, err := k.GetHistoricalInfo(ctx, height); err == nil {
			historicalInfo = hi
			found = true
			break
		}
	}

	if !found {
		return k.shareRepartitionMapFallback(ctx, req)
	}

	stakingAssets := k.erc20Keeper.GetAllStakingAssets(sdkCtx)
	shareRepartitionMap := make(map[string]types.SharesRepartition, len(stakingAssets))

	// Initialize shares map
	for _, consensusAsset := range stakingAssets {
		denom := consensusAsset.GetDenom()
		shareRepartitionMap[denom] = types.SharesRepartition{
			Denom:                         denom,
			ContractAddress:               consensusAsset.GetContractAddress(),
			BaseWeight:                    consensusAsset.GetBaseWeight(),
			NetworkShares:                 math.NewIntFromUint64(0),
			NetworkPercentageSecurisation: "0%",
		}
	}

	// Aggregate pre-calculated asset weights
	for _, validator := range historicalInfo.Valset {
		for _, assetWeight := range validator.TotalAssetWeights {
			if shareRepartition, exists := shareRepartitionMap[assetWeight.Denom]; exists {
				shareRepartition.NetworkShares = shareRepartition.NetworkShares.Add(assetWeight.WeightedAmount)
				shareRepartitionMap[assetWeight.Denom] = shareRepartition
			}
		}
	}

	// Aggregate pre-calculated asset weights from inactive validators
	for _, validator := range historicalInfo.InactiveValset {
		for _, assetWeight := range validator.TotalAssetWeights {
			if shareRepartition, exists := shareRepartitionMap[assetWeight.Denom]; exists {
				shareRepartition.NetworkShares = shareRepartition.NetworkShares.Add(assetWeight.WeightedAmount)
				shareRepartitionMap[assetWeight.Denom] = shareRepartition
			}
		}
	}

	// Calculate percentages
	totalShares := math.NewIntFromUint64(0)
	for _, consensusAsset := range stakingAssets {
		shareRepartition, exists := shareRepartitionMap[consensusAsset.GetDenom()]
		if !exists {
			continue
		}
		totalShares = totalShares.Add(shareRepartition.NetworkShares)
	}

	for _, consensusAsset := range stakingAssets {
		shareRepartition, exists := shareRepartitionMap[consensusAsset.GetDenom()]
		if !exists {
			continue
		}
		if totalShares.LTE(math.NewIntFromUint64(0)) {
			shareRepartition.NetworkPercentageSecurisation = "0%"
		} else {
			percentage := shareRepartition.NetworkShares.ToLegacyDec().Mul(math.NewIntFromUint64(100).ToLegacyDec()).Quo(totalShares.ToLegacyDec())
			shareRepartition.NetworkPercentageSecurisation = fmt.Sprintf("%f%%", percentage)
		}
		shareRepartitionMap[consensusAsset.GetDenom()] = shareRepartition
	}

	return &types.QueryShareRepartitionMapResponse{SharesRepartitionMap: shareRepartitionMap}, nil
}

func (k Querier) shareRepartitionMapFallback(ctx context.Context, _req *types.QueryShareRepartitionMapRequest) (*types.QueryShareRepartitionMapResponse, error) {
	res, err := k.Validators(ctx, &types.QueryValidatorsRequest{})
	if err != nil {
		return nil, err
	}

	stakingAssets := k.erc20Keeper.GetAllStakingAssets(sdk.UnwrapSDKContext(ctx))
	shareRepartitionMap := make(map[string]types.SharesRepartition, len(stakingAssets))

	// Initialize shares map
	for _, consensusAsset := range stakingAssets {
		denom := consensusAsset.GetDenom()
		shareRepartitionMap[denom] = types.SharesRepartition{
			Denom:                         denom,
			ContractAddress:               consensusAsset.GetContractAddress(),
			BaseWeight:                    consensusAsset.GetBaseWeight(),
			NetworkShares:                 math.NewIntFromUint64(0),
			NetworkPercentageSecurisation: "0%",
		}
	}

	// Aggregate delegations data
	for _, validator := range res.Validators {
		delegationsRes, err := k.ValidatorDelegations(ctx, &types.QueryValidatorDelegationsRequest{ValidatorAddr: validator.OperatorAddress})
		if err != nil {
			continue
		}
		for _, delegationRes := range delegationsRes.DelegationResponses {
			delegation := delegationRes.GetDelegation()
			for _, asset := range delegation.AssetWeights {
				if shareRepartition, exists := shareRepartitionMap[asset.Denom]; exists {
					shareRepartition.NetworkShares = shareRepartition.NetworkShares.Add(asset.WeightedAmount)

					sdk.UnwrapSDKContext(ctx).Logger().Debug("Asset", "asset.WeightedAmount", asset.WeightedAmount, "shareRepartition.NetworkShares", shareRepartition.NetworkShares)
					shareRepartitionMap[asset.Denom] = shareRepartition
				}
			}
		}
	}

	// Calculate percentages - EXACTEMENT comme ton code original
	totalShares := math.NewIntFromUint64(0)
	for _, consensusAsset := range stakingAssets {
		shareRepartition, exists := shareRepartitionMap[consensusAsset.GetDenom()]
		if !exists {
			continue
		}
		totalShares = totalShares.Add(shareRepartition.NetworkShares)
	}

	for _, consensusAsset := range stakingAssets {
		shareRepartition, exists := shareRepartitionMap[consensusAsset.GetDenom()]
		if !exists {
			continue
		}
		if totalShares.LTE(math.NewIntFromUint64(0)) {
			shareRepartition.NetworkPercentageSecurisation = "0%"
		} else {
			percentage := shareRepartition.NetworkShares.ToLegacyDec().Mul(math.NewIntFromUint64(100).ToLegacyDec()).Quo(totalShares.ToLegacyDec())
			shareRepartition.NetworkPercentageSecurisation = fmt.Sprintf("%f%%", percentage)
		}
		shareRepartitionMap[consensusAsset.GetDenom()] = shareRepartition
	}

	return &types.QueryShareRepartitionMapResponse{SharesRepartitionMap: shareRepartitionMap}, nil
}

// Validator queries validator info for given validator address
func (k Querier) Validator(ctx context.Context, req *types.QueryValidatorRequest) (*types.QueryValidatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	validator, err := k.GetValidator(ctx, valAddr)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "validator %s not found", req.ValidatorAddr)
	}

	return &types.QueryValidatorResponse{Validator: validator}, nil
}

// ValidatorDelegations queries delegate info for given validator
func (k Querier) ValidatorDelegations(ctx context.Context, req *types.QueryValidatorDelegationsRequest) (*types.QueryValidatorDelegationsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	delStore := prefix.NewStore(store, types.GetDelegationsByValPrefixKey(valAddr))

	var (
		dels    types.Delegations
		pageRes *query.PageResponse
	)
	pageRes, err = query.Paginate(delStore, req.Pagination, func(delAddr, value []byte) error {
		bz := store.Get(types.GetDelegationKey(delAddr, valAddr))

		var delegation types.Delegation
		err = k.cdc.Unmarshal(bz, &delegation)
		if err != nil {
			return err
		}

		dels = append(dels, delegation)
		return nil
	})
	if err != nil {
		delegations, pageResponse, err := k.getValidatorDelegationsLegacy(ctx, req)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		dels = types.Delegations{}
		for _, d := range delegations {
			dels = append(dels, *d)
		}

		pageRes = pageResponse
	}

	delResponses, err := delegationsToDelegationResponses(ctx, k.Keeper, dels)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryValidatorDelegationsResponse{
		DelegationResponses: delResponses, Pagination: pageRes,
	}, nil
}

func (k Querier) getValidatorDelegationsLegacy(ctx context.Context, req *types.QueryValidatorDelegationsRequest) ([]*types.Delegation, *query.PageResponse, error) {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))

	valStore := prefix.NewStore(store, types.DelegationKey)
	return query.GenericFilteredPaginate(k.cdc, valStore, req.Pagination, func(key []byte, delegation *types.Delegation) (*types.Delegation, error) {
		_, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
		if err != nil {
			return nil, err
		}

		if !strings.EqualFold(delegation.GetValidatorAddr(), req.ValidatorAddr) {
			return nil, nil
		}

		return delegation, nil
	}, func() *types.Delegation {
		return &types.Delegation{}
	})
}

// ValidatorUnbondingDelegations queries unbonding delegations of a validator
func (k Querier) ValidatorUnbondingDelegations(ctx context.Context, req *types.QueryValidatorUnbondingDelegationsRequest) (*types.QueryValidatorUnbondingDelegationsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}
	var ubds types.UnbondingDelegations

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	srcValPrefix := types.GetUBDsByValIndexKey(valAddr)
	ubdStore := prefix.NewStore(store, srcValPrefix)
	pageRes, err := query.Paginate(ubdStore, req.Pagination, func(key, value []byte) error {
		storeKey := types.GetUBDKeyFromValIndexKey(append(srcValPrefix, key...))
		storeValue := store.Get(storeKey)

		ubd, err := types.UnmarshalUBD(k.cdc, storeValue)
		if err != nil {
			return err
		}
		ubds = append(ubds, ubd)
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryValidatorUnbondingDelegationsResponse{
		UnbondingResponses: ubds,
		Pagination:         pageRes,
	}, nil
}

func (k Querier) GetDelegations(ctx context.Context, req *types.QueryGetDelegationsRequest) (*types.QueryGetDelegationsResponse, error) {
	delegations := make([]types.Delegation, 0)
	totalVotingPower := math.LegacyZeroDec()
	delegatorAddress, err := sdk.AccAddressFromBech32(req.DelegatorAddr)

	if err != nil {
		return &types.QueryGetDelegationsResponse{Delegations: delegations}, status.Error(codes.InvalidArgument, "delegator address parsing failed")
	}
	// iterate over all delegations from voter, deduct from any delegated-to validators
	err = k.IterateDelegations(ctx, delegatorAddress, func(index int64, delegation types.DelegationI) (stop bool) {

		votingPower := delegation.GetShares() //.MulInt(val.BondedTokens).Quo(val.DelegatorShares)
		totalVotingPower = totalVotingPower.Add(votingPower)

		delegationData := types.Delegation{
			DelegatorAddress:    delegation.GetDelegatorAddr(),
			ValidatorAddress:    delegation.GetValidatorAddr(),
			Shares:              delegation.GetShares(),
			AssetWeights:        delegation.GetAssetWeight(),
			TotalWeightedAmount: delegation.GetTotalWeightedAmount(),
		}
		delegations = append(delegations, delegationData)
		return false
	})
	if err != nil {
		return &types.QueryGetDelegationsResponse{Delegations: delegations}, err
	}
	return &types.QueryGetDelegationsResponse{Delegations: delegations}, nil
}

// Delegation queries delegate info for given validator delegator pair
func (k Querier) Delegation(ctx context.Context, req *types.QueryDelegationRequest) (*types.QueryDelegationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.DelegatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "delegator address cannot be empty")
	}
	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	delegation, err := k.GetDelegation(ctx, delAddr, valAddr)
	if err != nil {
		return nil, status.Errorf(
			codes.NotFound,
			"delegation with delegator %s not found for validator %s",
			req.DelegatorAddr, req.ValidatorAddr)
	}

	delResponse, err := delegationToDelegationResponse(ctx, k.Keeper, delegation)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryDelegationResponse{DelegationResponse: &delResponse}, nil
}

// UnbondingDelegation queries unbonding info for given validator delegator pair
func (k Querier) UnbondingDelegation(ctx context.Context, req *types.QueryUnbondingDelegationRequest) (*types.QueryUnbondingDelegationResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if req.DelegatorAddr == "" {
		return nil, status.Errorf(codes.InvalidArgument, "delegator address cannot be empty")
	}
	if req.ValidatorAddr == "" {
		return nil, status.Errorf(codes.InvalidArgument, "validator address cannot be empty")
	}

	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	unbond, err := k.GetUnbondingDelegation(ctx, delAddr, valAddr)
	if err != nil {
		return nil, status.Errorf(
			codes.NotFound,
			"unbonding delegation with delegator %s not found for validator %s",
			req.DelegatorAddr, req.ValidatorAddr)
	}

	return &types.QueryUnbondingDelegationResponse{Unbond: unbond}, nil
}

// DelegatorDelegations queries all delegations of a given delegator address
func (k Querier) DelegatorDelegations(ctx context.Context, req *types.QueryDelegatorDelegationsRequest) (*types.QueryDelegatorDelegationsResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if req.DelegatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "delegator address cannot be empty")
	}
	var delegations types.Delegations

	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	delStore := prefix.NewStore(store, types.GetDelegationsKey(delAddr))
	pageRes, err := query.Paginate(delStore, req.Pagination, func(key, value []byte) error {
		delegation, err := types.UnmarshalDelegation(k.cdc, value)
		if err != nil {
			return err
		}
		delegations = append(delegations, delegation)
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	delegationResps, err := delegationsToDelegationResponses(ctx, k.Keeper, delegations)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryDelegatorDelegationsResponse{DelegationResponses: delegationResps, Pagination: pageRes}, nil
}

// DelegatorValidator queries validator info for given delegator validator pair
func (k Querier) DelegatorValidator(ctx context.Context, req *types.QueryDelegatorValidatorRequest) (*types.QueryDelegatorValidatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.DelegatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "delegator address cannot be empty")
	}
	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	validator, err := k.GetDelegatorValidator(ctx, delAddr, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryDelegatorValidatorResponse{Validator: validator}, nil
}

func (k Querier) TotalBoostedDelegations(ctx context.Context, req *types.QueryTotalBoostedDelegationsRequest) (*types.QueryTotalBoostedDelegationsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.DelegatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "delegator address cannot be empty")
	}

	delAddr, err := sdk.AccAddressFromBech32(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	delegationBoosts, err := k.GetTotalBoostedDelegations(ctx, delAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryTotalBoostedDelegationsResponse{
		DelegationBoosts: delegationBoosts,
	}, nil
}

func (k Querier) ValidatorAssets(ctx context.Context, req *types.QueryValidatorAssetsRequest) (*types.QueryValidatorAssetsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	_, err = k.GetValidator(ctx, valAddr)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "validator %s not found", req.ValidatorAddr)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := sdkCtx.BlockHeight()

	for height := currentHeight; height >= currentHeight-3; height-- {
		if hi, err := k.GetHistoricalInfo(ctx, height); err == nil {
			for _, validator := range hi.Valset {
				if validator.OperatorAddress == req.ValidatorAddr {
					return &types.QueryValidatorAssetsResponse{Assets: validator.TotalAssetWeights}, nil
				}
			}
		}
	}

	return &types.QueryValidatorAssetsResponse{Assets: []types.AssetWeight{}}, nil
}

func (k Querier) TotalBoostedValidator(ctx context.Context, req *types.QueryTotalBoostedValidatorRequest) (*types.QueryTotalBoostedValidatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	val, err := k.GetValidator(ctx, valAddr)
	if err != nil {
		return nil, err
	}

	totalStaked := math.LegacyNewDecFromInt(val.GetTokens())
	if totalStaked.LTE(math.LegacyNewDecFromInt(math.NewInt(0))) {
		totalStaked = math.LegacyNewDecFromInt(math.NewInt(1))
	}

	totalBoost, err := k.GetTotalBoostedValidator(ctx, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	weightedTotalBoost, err := k.ConvertAssetToSDKCoin(sdkCtx, sdk.DefaultBondDenom, totalBoost.TruncateInt())

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	boostRatio := weightedTotalBoost.Amount.ToLegacyDec().Quo(totalStaked)
	if boostRatio.GT(math.LegacyOneDec()) {
		boostRatio = math.LegacyOneDec()
	}

	boostPercentage := boostRatio.Mul(math.LegacyNewDec(100))

	return &types.QueryTotalBoostedValidatorResponse{
		TotalBoost:      totalBoost.String(),
		BoostPercentage: boostPercentage.String(),
	}, nil
}

func (k Querier) TotalBoostedDelegation(ctx context.Context, req *types.QueryTotalBoostedDelegationRequest) (*types.QueryTotalBoostedDelegationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.ValidatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "validator address cannot be empty")
	}

	valAddr, err := k.validatorAddressCodec.StringToBytes(req.ValidatorAddr)
	if err != nil {
		return nil, err
	}

	delAddr, err := sdk.AccAddressFromBech32(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	totalBoost, err := k.GetTotalBoostedDelegation(ctx, delAddr, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryTotalBoostedDelegationResponse{
		TotalBoost: totalBoost.Amount.String(),
	}, nil
}

// DelegatorUnbondingDelegations queries all unbonding delegations of a given delegator address
func (k Querier) DelegatorUnbondingDelegations(ctx context.Context, req *types.QueryDelegatorUnbondingDelegationsRequest) (*types.QueryDelegatorUnbondingDelegationsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.DelegatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "delegator address cannot be empty")
	}
	var unbondingDelegations types.UnbondingDelegations

	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	unbStore := prefix.NewStore(store, types.GetUBDsKey(delAddr))
	pageRes, err := query.Paginate(unbStore, req.Pagination, func(key, value []byte) error {
		unbond, err := types.UnmarshalUBD(k.cdc, value)
		if err != nil {
			return err
		}
		unbondingDelegations = append(unbondingDelegations, unbond)
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryDelegatorUnbondingDelegationsResponse{
		UnbondingResponses: unbondingDelegations, Pagination: pageRes,
	}, nil
}

// HistoricalInfo queries the historical info for given height
func (k Querier) HistoricalInfo(ctx context.Context, req *types.QueryHistoricalInfoRequest) (*types.QueryHistoricalInfoResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.Height < 0 {
		return nil, status.Error(codes.InvalidArgument, "height cannot be negative")
	}

	hi, err := k.GetHistoricalInfo(ctx, req.Height)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "historical info for height %d not found", req.Height)
	}

	return &types.QueryHistoricalInfoResponse{Hist: &hi}, nil
}

// Redelegations queries redelegations of given address
func (k Querier) Redelegations(ctx context.Context, req *types.QueryRedelegationsRequest) (*types.QueryRedelegationsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	var redels types.Redelegations
	var pageRes *query.PageResponse
	var err error

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	switch {
	case req.DelegatorAddr != "" && req.SrcValidatorAddr != "" && req.DstValidatorAddr != "":
		redels, err = queryRedelegation(ctx, k, req)
	case req.DelegatorAddr == "" && req.SrcValidatorAddr != "" && req.DstValidatorAddr == "":
		redels, pageRes, err = queryRedelegationsFromSrcValidator(store, k, req)
	default:
		redels, pageRes, err = queryAllRedelegations(store, k, req)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	redelResponses, err := redelegationsToRedelegationResponses(ctx, k.Keeper, redels)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryRedelegationsResponse{RedelegationResponses: redelResponses, Pagination: pageRes}, nil
}

// DelegatorValidators queries all validators info for given delegator address
func (k Querier) DelegatorValidators(ctx context.Context, req *types.QueryDelegatorValidatorsRequest) (*types.QueryDelegatorValidatorsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if req.DelegatorAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "delegator address cannot be empty")
	}
	var validators types.Validators

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	delStore := prefix.NewStore(store, types.GetDelegationsKey(delAddr))
	pageRes, err := query.Paginate(delStore, req.Pagination, func(key, value []byte) error {
		delegation, err := types.UnmarshalDelegation(k.cdc, value)
		if err != nil {
			return err
		}

		valAddr, err := k.validatorAddressCodec.StringToBytes(delegation.GetValidatorAddr())
		if err != nil {
			return err
		}

		validator, err := k.GetValidator(ctx, valAddr)
		if err != nil {
			return err
		}

		validators.Validators = append(validators.Validators, validator)
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryDelegatorValidatorsResponse{Validators: validators.Validators, Pagination: pageRes}, nil
}

// Pool queries the pool info
func (k Querier) Pool(ctx context.Context, _ *types.QueryPoolRequest) (*types.QueryPoolResponse, error) {
	bondDenom, err := k.BondDenom(ctx)
	if err != nil {
		return nil, err
	}
	bondedPool := k.GetBondedPool(ctx)
	notBondedPool := k.GetNotBondedPool(ctx)
	boostedPool := k.GetBoostedPool(ctx)

	pool := types.NewPool(
		k.bankKeeper.GetBalance(ctx, notBondedPool.GetAddress(), bondDenom).Amount,
		k.bankKeeper.GetBalance(ctx, bondedPool.GetAddress(), bondDenom).Amount,
		k.bankKeeper.GetBalance(ctx, boostedPool.GetAddress(), bondDenom).Amount,
	)

	return &types.QueryPoolResponse{Pool: pool}, nil
}

// Params queries the staking parameters
func (k Querier) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

func queryRedelegation(ctx context.Context, k Querier, req *types.QueryRedelegationsRequest) (redels types.Redelegations, err error) {
	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, err
	}

	srcValAddr, err := k.validatorAddressCodec.StringToBytes(req.SrcValidatorAddr)
	if err != nil {
		return nil, err
	}

	dstValAddr, err := k.validatorAddressCodec.StringToBytes(req.DstValidatorAddr)
	if err != nil {
		return nil, err
	}

	redel, err := k.GetRedelegation(ctx, delAddr, srcValAddr, dstValAddr)
	if err != nil {
		return nil, status.Errorf(
			codes.NotFound,
			"redelegation not found for delegator address %s from validator address %s",
			req.DelegatorAddr, req.SrcValidatorAddr)
	}
	redels = []types.Redelegation{redel}

	return redels, nil
}

func queryRedelegationsFromSrcValidator(store storetypes.KVStore, k Querier, req *types.QueryRedelegationsRequest) (redels types.Redelegations, res *query.PageResponse, err error) {
	valAddr, err := k.validatorAddressCodec.StringToBytes(req.SrcValidatorAddr)
	if err != nil {
		return nil, nil, err
	}

	srcValPrefix := types.GetREDsFromValSrcIndexKey(valAddr)
	redStore := prefix.NewStore(store, srcValPrefix)
	res, err = query.Paginate(redStore, req.Pagination, func(key, value []byte) error {
		storeKey := types.GetREDKeyFromValSrcIndexKey(append(srcValPrefix, key...))
		storeValue := store.Get(storeKey)
		red, err := types.UnmarshalRED(k.cdc, storeValue)
		if err != nil {
			return err
		}
		redels = append(redels, red)
		return nil
	})

	return redels, res, err
}

func queryAllRedelegations(store storetypes.KVStore, k Querier, req *types.QueryRedelegationsRequest) (redels types.Redelegations, res *query.PageResponse, err error) {
	delAddr, err := k.authKeeper.AddressCodec().StringToBytes(req.DelegatorAddr)
	if err != nil {
		return nil, nil, err
	}

	redStore := prefix.NewStore(store, types.GetREDsKey(delAddr))
	res, err = query.Paginate(redStore, req.Pagination, func(key, value []byte) error {
		redelegation, err := types.UnmarshalRED(k.cdc, value)
		if err != nil {
			return err
		}
		redels = append(redels, redelegation)
		return nil
	})

	return redels, res, err
}

// util

func delegationToDelegationResponse(ctx context.Context, k *Keeper, del types.Delegation) (types.DelegationResponse, error) {
	valAddr, err := k.validatorAddressCodec.StringToBytes(del.GetValidatorAddr())
	if err != nil {
		return types.DelegationResponse{}, err
	}

	val, err := k.GetValidator(ctx, valAddr)
	if err != nil {
		return types.DelegationResponse{}, err
	}

	_, err = k.authKeeper.AddressCodec().StringToBytes(del.DelegatorAddress)
	if err != nil {
		return types.DelegationResponse{}, err
	}

	bondDenom, err := k.BondDenom(ctx)
	if err != nil {
		return types.DelegationResponse{}, err
	}

	return types.NewDelegationResp(
		del.DelegatorAddress,
		del.GetValidatorAddr(),
		del.Shares,
		del.AssetWeights,
		del.TotalWeightedAmount,
		sdk.NewCoin(bondDenom, val.TokensFromShares(del.Shares).TruncateInt()),
	), nil
}

func delegationsToDelegationResponses(ctx context.Context, k *Keeper, delegations types.Delegations) (types.DelegationResponses, error) {
	resp := make(types.DelegationResponses, len(delegations))

	for i, del := range delegations {
		delResp, err := delegationToDelegationResponse(ctx, k, del)
		if err != nil {
			return nil, err
		}

		resp[i] = delResp
	}

	return resp, nil
}

func redelegationsToRedelegationResponses(ctx context.Context, k *Keeper, redels types.Redelegations) (types.RedelegationResponses, error) {
	resp := make(types.RedelegationResponses, len(redels))

	for i, redel := range redels {
		_, err := k.validatorAddressCodec.StringToBytes(redel.ValidatorSrcAddress)
		if err != nil {
			return nil, err
		}
		valDstAddr, err := k.validatorAddressCodec.StringToBytes(redel.ValidatorDstAddress)
		if err != nil {
			return nil, err
		}

		_, err = k.authKeeper.AddressCodec().StringToBytes(redel.DelegatorAddress)
		if err != nil {
			return nil, err
		}

		val, err := k.GetValidator(ctx, valDstAddr)
		if err != nil {
			return nil, err
		}

		entryResponses := make([]types.RedelegationEntryResponse, len(redel.Entries))
		for j, entry := range redel.Entries {
			entryResponses[j] = types.NewRedelegationEntryResponse(
				entry.CreationHeight,
				entry.CompletionTime,
				entry.SharesDst,
				entry.InitialBalance,
				val.TokensFromShares(entry.SharesDst).TruncateInt(),
				entry.UnbondingId,
			)
		}

		resp[i] = types.NewRedelegationResponse(
			redel.DelegatorAddress,
			redel.ValidatorSrcAddress,
			redel.ValidatorDstAddress,
			entryResponses,
		)
	}

	return resp, nil
}

// EpochInfo returns current epoch information
func (k Keeper) EpochInfo(ctx context.Context, req *types.QueryEpochInfoRequest) (*types.QueryEpochInfoResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	// Get epoch configuration and state
	currentEpoch := k.GetCurrentEpoch(ctx)
	epochLength := k.GetEpochLength(ctx)
	lastEpochHeight := k.GetLastEpochHeight(ctx)
	validatorsPerEpoch := k.GetValidatorsPerEpoch(ctx)
	epochEnabled := k.IsEpochEnabled(ctx)

	// Get current height
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := uint64(sdkCtx.BlockHeight())

	// Calculate blocks until next epoch
	var blocksUntilNextEpoch uint64
	if lastEpochHeight > 0 && epochLength > 0 {
		nextEpochHeight := lastEpochHeight + epochLength
		if currentHeight < nextEpochHeight {
			blocksUntilNextEpoch = nextEpochHeight - currentHeight
		} else {
			blocksUntilNextEpoch = 0 // We're at or past the next epoch height
		}
	} else {
		blocksUntilNextEpoch = epochLength // First epoch
	}

	return &types.QueryEpochInfoResponse{
		CurrentEpoch:         currentEpoch,
		EpochLength:          epochLength,
		LastEpochHeight:      lastEpochHeight,
		ValidatorsPerEpoch:   validatorsPerEpoch,
		EpochEnabled:         epochEnabled,
		CurrentHeight:        currentHeight,
		BlocksUntilNextEpoch: blocksUntilNextEpoch,
	}, nil
}

func (k Keeper) GetEpochValidatorsHandler(ctx context.Context, req *types.QueryEpochValidatorsRequest) (*types.QueryEpochValidatorsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Check if epoch is enabled
	if !k.IsEpochEnabled(sdkCtx) {
		return nil, status.Error(codes.FailedPrecondition, "epoch-based validator rotation is not enabled")
	}

	// Get current epoch for validation
	currentEpoch := k.GetCurrentEpoch(sdkCtx)

	// If no specific epoch is requested or epoch is 0, use current epoch
	epochToQuery := currentEpoch

	// Validate requested epoch is not in the future
	if epochToQuery > currentEpoch {
		return nil, status.Error(codes.InvalidArgument, "requested epoch is in the future")
	}

	// Get validators for the requested epoch
	var validators []types.Validator
	if epochToQuery == currentEpoch {
		// For current epoch, use the active validators function
		validators = k.GetActiveValidatorsForCurrentEpoch(sdkCtx)
	} else {
		// For historical epochs, use the epoch history function
		validators = k.GetEpochValidators(sdkCtx, epochToQuery)
	}

	// If no validators found, return empty list
	if len(validators) == 0 {
		return &types.QueryEpochValidatorsResponse{
			Validators: []types.Validator{},
			Pagination: &query.PageResponse{
				Total: 0,
			},
		}, nil
	}

	// Handle pagination
	start, end := uint64(0), uint64(len(validators))
	if req.Pagination != nil {
		if req.Pagination.Offset > 0 {
			start = req.Pagination.Offset
		}

		if req.Pagination.Limit > 0 {
			end = start + req.Pagination.Limit
			if end > uint64(len(validators)) {
				end = uint64(len(validators))
			}
		}
	}

	// Apply pagination
	var paginatedResults []types.Validator
	if start < uint64(len(validators)) {
		paginatedResults = validators[start:end]
	} else {
		paginatedResults = []types.Validator{}
	}

	// Prepare pagination response
	var nextKey []byte
	if end < uint64(len(validators)) {
		// Only set nextKey if there are more results
		nextKeyUint64 := end
		nextKey = sdk.Uint64ToBigEndian(nextKeyUint64)
	}

	pageRes := &query.PageResponse{
		NextKey: nextKey,
		Total:   uint64(len(validators)),
	}

	return &types.QueryEpochValidatorsResponse{
		Validators: paginatedResults,
		Pagination: pageRes,
	}, nil
}

// GetPreviousEpochValidators returns the list of validators from the previous epoch
func (k Keeper) GetPreviousEpochValidatorsHandler(ctx context.Context, req *types.QueryPreviousEpochValidatorsRequest) (*types.QueryPreviousEpochValidatorsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Check if epoch is enabled
	if !k.IsEpochEnabled(sdkCtx) {
		return nil, status.Error(codes.FailedPrecondition, "epoch-based validator rotation is not enabled")
	}

	// Get current epoch for validation
	currentEpoch := k.GetCurrentEpoch(sdkCtx)

	// Check if we have a previous epoch (current epoch > 1)
	if currentEpoch <= 1 {
		return nil, status.Error(codes.NotFound, "no previous epoch exists yet")
	}

	// Get the previous epoch validators directly
	previousValidators := k.GetAllPreviouslyActiveValidators(sdkCtx)

	// If no validators found, return empty list
	if len(previousValidators) == 0 {
		return &types.QueryPreviousEpochValidatorsResponse{
			Validators: []types.Validator{},
			Pagination: &query.PageResponse{
				Total: 0,
			},
		}, nil
	}

	// Handle pagination
	start, end := uint64(0), uint64(len(previousValidators))
	if req.Pagination != nil {
		if req.Pagination.Offset > 0 {
			start = req.Pagination.Offset
		}

		if req.Pagination.Limit > 0 {
			end = start + req.Pagination.Limit
			if end > uint64(len(previousValidators)) {
				end = uint64(len(previousValidators))
			}
		}
	}

	// Apply pagination
	var paginatedResults []types.Validator
	if start < uint64(len(previousValidators)) {
		paginatedResults = previousValidators[start:end]
	} else {
		paginatedResults = []types.Validator{}
	}

	// Prepare pagination response
	var nextKey []byte
	if end < uint64(len(previousValidators)) {
		// Only set nextKey if there are more results
		nextKeyUint64 := end
		nextKey = sdk.Uint64ToBigEndian(nextKeyUint64)
	}

	pageRes := &query.PageResponse{
		NextKey: nextKey,
		Total:   uint64(len(previousValidators)),
	}

	return &types.QueryPreviousEpochValidatorsResponse{
		Validators: paginatedResults,
		Pagination: pageRes,
	}, nil
}

// GetEpochLength returns the configured epoch length in blocks
func (k Keeper) GetEpochLengthHandler(ctx context.Context, req *types.QueryEpochLengthRequest) (*types.QueryEpochLengthResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	epochLength := k.GetEpochLength(sdkCtx)

	return &types.QueryEpochLengthResponse{
		EpochLength: epochLength,
	}, nil
}

// GetValidatorsPerEpoch returns the number of validators selected per epoch
func (k Keeper) GetValidatorsPerEpochHandler(ctx context.Context, req *types.QueryValidatorsPerEpochRequest) (*types.QueryValidatorsPerEpochResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	validatorsPerEpoch := k.GetValidatorsPerEpoch(sdkCtx)

	return &types.QueryValidatorsPerEpochResponse{
		ValidatorsPerEpoch: validatorsPerEpoch,
	}, nil
}

// IsEpochEnabled checks if epoch-based validator rotation is enabled
func (k Keeper) GetIsEpochEnabledHandler(ctx context.Context, req *types.QueryIsEpochEnabledRequest) (*types.QueryIsEpochEnabledResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	enabled := k.IsEpochEnabled(sdkCtx)

	return &types.QueryIsEpochEnabledResponse{
		EpochEnabled: enabled,
	}, nil
}

// GetCurrentEpochHandler returns current epoch information
func (k Keeper) GetCurrentEpochHandler(ctx context.Context, req *types.QueryCurrentEpochRequest) (*types.QueryCurrentEpochResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Get all required information from the keeper
	currentEpoch := k.GetCurrentEpoch(sdkCtx)
	epochLength := k.GetEpochLength(sdkCtx)
	lastEpochHeight := k.GetLastEpochHeight(sdkCtx)
	validatorsPerEpoch := k.GetValidatorsPerEpoch(sdkCtx)
	epochEnabled := k.IsEpochEnabled(sdkCtx)

	return &types.QueryCurrentEpochResponse{
		CurrentEpoch:       currentEpoch,
		EpochLength:        epochLength,
		LastEpochHeight:    lastEpochHeight,
		ValidatorsPerEpoch: validatorsPerEpoch,
		EpochEnabled:       epochEnabled,
	}, nil
}

func (k Querier) BlockSignatures(ctx context.Context, req *types.QueryBlockSignaturesRequest) (*types.QueryBlockSignaturesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.Height <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid block height")
	}

	// Get historical information
	hist, err := k.GetHistoricalInfo(ctx, req.Height)
	if err != nil {
		return nil, status.Errorf(
			codes.NotFound,
			"no historical info found at height %d",
			req.Height,
		)
	}

	// Use historical context for the requested height
	historicalCtx := sdk.UnwrapSDKContext(ctx).WithBlockHeight(req.Height)

	// Access getSlashingKeeper through the embedded *Keeper
	if k.Keeper == nil {
		return nil, status.Error(codes.Internal, "keeper is nil")
	}
	slashingKeeper := k.Keeper.getSlashingKeeper()
	if slashingKeeper == nil {
		return nil, status.Error(codes.Internal, "slashing keeper not set")
	}

	// Get signing window parameter at historical height
	params, err := slashingKeeper.GetParams(historicalCtx)
	if err != nil {
		return nil, err
	}
	signingWindow := params.SignedBlocksWindow

	// Calculate epoch information
	epochLength := k.GetEpochLength(historicalCtx)
	var currentEpoch uint64
	var blocksValidated, blocksRemaining int64

	if epochLength > 0 {
		currentEpoch = uint64(req.Height) / epochLength
		epochStart := int64(currentEpoch * epochLength)
		blocksValidated = req.Height - epochStart
		blocksRemaining = int64(epochLength) - (req.Height % int64(epochLength))
	}

	// Process each validator
	var signatures []*types.ValidatorSigningStatus
	for _, val := range hist.Valset {
		consAddrBz, err := val.GetConsAddr()
		if err != nil {
			signatures = append(signatures, &types.ValidatorSigningStatus{
				Address:          "",
				Signed:           false,
				IndexOffset:      0,
				TotalTokens:      "0",
				AssetWeights:     nil,
				EpochNumber:      int64(currentEpoch),
				Status:           "UNKNOWN",
				Jailed:           false,
				MissedBlockCount: 0,
			})
			continue
		}

		if err != nil {
			signatures = append(signatures, &types.ValidatorSigningStatus{
				Address:          "",
				Signed:           false,
				IndexOffset:      0,
				TotalTokens:      "0",
				AssetWeights:     nil,
				EpochNumber:      int64(currentEpoch),
				Status:           "UNKNOWN",
				Jailed:           false,
				MissedBlockCount: 0,
			})
			continue
		}

		// Get validator signing info at historical height
		info, err := slashingKeeper.GetValidatorSigningInfo(historicalCtx, consAddrBz)
		if err != nil {
			signatures = append(signatures, &types.ValidatorSigningStatus{
				Address:          val.OperatorAddress,
				Signed:           false,
				IndexOffset:      0,
				TotalTokens:      val.Tokens.String(),
				AssetWeights:     convertAssetWeights(val.TotalAssetWeights),
				EpochNumber:      int64(currentEpoch),
				Status:           val.Status.String(), // Use historical status
				Jailed:           val.Jailed,          // Use historical jailed status
				MissedBlockCount: 0,
			})
			continue
		}

		// Check if the validator missed this specific block
		index := req.Height % signingWindow
		missed, err := slashingKeeper.GetMissedBlockBitmapValue(historicalCtx, consAddrBz, index)
		if err != nil {
			missed = true // Default to missed if we can't retrieve the value
		}

		// Get missed block count at historical height
		missedBlockCount := info.MissedBlocksCounter

		signatures = append(signatures, &types.ValidatorSigningStatus{
			Address:          val.OperatorAddress,
			Signed:           !missed,
			IndexOffset:      info.IndexOffset,
			TotalTokens:      val.Tokens.String(),
			AssetWeights:     convertAssetWeights(val.TotalAssetWeights),
			EpochNumber:      int64(currentEpoch),
			Status:           val.Status.String(), // Use historical status from val
			Jailed:           val.Jailed,          // Use historical jailed from val
			MissedBlockCount: missedBlockCount,
		})
	}

	return &types.QueryBlockSignaturesResponse{
		Signatures:           signatures,
		CurrentEpoch:         int64(currentEpoch),
		EpochBlocksValidated: blocksValidated,
		EpochBlocksRemaining: blocksRemaining,
		BlockHeight:          req.Height,
	}, nil
}

// Helper function to convert AssetWeight slice
func convertAssetWeights(assetWeights []types.AssetWeight) []*types.AssetWeight {
	if assetWeights == nil {
		return nil
	}
	result := make([]*types.AssetWeight, len(assetWeights))
	for i, aw := range assetWeights {
		result[i] = &types.AssetWeight{
			Denom:          aw.Denom,          // Keep as string
			BaseAmount:     aw.BaseAmount,     // Keep as math.Int
			WeightedAmount: aw.WeightedAmount, // Keep as math.Int
		}
	}
	return result
}

// EpochComplete returns complete information about a specific epoch with all validators details
func (k Querier) EpochComplete(ctx context.Context, req *types.QueryEpochCompleteRequest) (*types.QueryEpochCompleteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := sdkCtx.BlockHeight()
	epochLength := k.GetEpochLength(sdkCtx)

	var targetEpoch uint64 = req.EpochId
	// Calculate epoch boundaries
	startHeight := int64(targetEpoch * epochLength)
	endHeight := startHeight + int64(epochLength) - 1

	// Create historical context for the epoch start
	historicalCtx := sdkCtx.WithBlockHeight(startHeight)

	// Calculate progress in current epoch
	blocksValidated := int64(0)
	blocksRemaining := int64(epochLength)
	if currentHeight >= startHeight && currentHeight <= endHeight {
		blocksValidated = currentHeight - startHeight + 1
		blocksRemaining = endHeight - currentHeight
	} else if currentHeight > endHeight {
		blocksValidated = int64(epochLength)
		blocksRemaining = 0
	}

	// Calculate blocks until next epoch
	currentEpoch := uint64(currentHeight) / epochLength
	blocksUntilNext := int64(0)
	if currentEpoch >= targetEpoch {
		nextEpochStart := int64((currentEpoch + 1) * epochLength)
		blocksUntilNext = nextEpochStart - currentHeight
	}

	slashingKeeper := k.Keeper.getSlashingKeeper()
	if slashingKeeper == nil {
		return nil, status.Error(codes.Internal, "slashing keeper not set")
	}

	// Get validators using historical data for the target epoch
	hist, err := k.GetHistoricalInfo(ctx, startHeight)
	if err != nil {
		// If no historical info at exact start, try to find the nearest one within epoch
		found := false
		for i := startHeight + 1; i <= endHeight && i <= currentHeight; i++ {
			hist, err = k.GetHistoricalInfo(ctx, i)
			if err == nil {
				// Update startHeight to match found historical data
				startHeight = i
				found = true
				break
			}
		}

		if !found {
			return nil, status.Errorf(codes.NotFound, "no historical info for epoch %d", targetEpoch)
		}
	}

	validators := hist.Valset

	var validatorDetails []*types.EpochValidatorDetail
	totalTokens := math.ZeroInt()
	totalVotingPower := math.ZeroInt()

	for _, val := range validators {
		consAddrBz, err := val.GetConsAddr()
		if err != nil {
			continue
		}

		consAddr, err := k.ConsensusAddressCodec().BytesToString(consAddrBz)
		if err != nil {
			continue
		}

		operatorAddr := val.OperatorAddress

		// Get signing information from slashing keeper using historical context
		missedBlockCount := int64(0)
		startHeightStr := "0"

		// Historical signing info might not be available for all heights
		// We use what's available but don't error if missing
		info, err := slashingKeeper.GetValidatorSigningInfo(historicalCtx, consAddrBz)
		if err == nil {
			missedBlockCount = info.MissedBlocksCounter
			startHeightStr = strconv.FormatInt(info.StartHeight, 10)
		}

		// Get signing window parameter from historical context
		signingWindow := int64(100) // Default value
		params, err := slashingKeeper.GetParams(historicalCtx)
		if err == nil {
			signingWindow = params.SignedBlocksWindow
		}

		// Collect signing history for this epoch
		var blocksSigned []*types.EpochValidatorSignature
		var blocksMissed []int64

		// Scan through the epoch to get signing history - only for heights we have data for
		maxHeight := endHeight
		if currentHeight < endHeight {
			maxHeight = currentHeight
		}

		for h := startHeight; h <= maxHeight; h++ {
			// Create context for each specific block height
			blockCtx := sdkCtx.WithBlockHeight(h)

			index := h % signingWindow
			missed, err := slashingKeeper.GetMissedBlockBitmapValue(blockCtx, consAddrBz, index)
			if err != nil {
				// If we can't get the data, we don't include it
				continue
			}

			if missed {
				blocksMissed = append(blocksMissed, h)
			} else {
				blocksSigned = append(blocksSigned, &types.EpochValidatorSignature{
					Signature: true,
					Height:    h,
				})
			}
		}

		// Use the validator status from the historical data
		status := val.Status.String()
		isSlashed := val.Jailed
		isJailed := val.Jailed
		commissionRate := val.Commission.Rate.String()

		// Calculate voting power using historical data
		votingPower := val.Tokens

		totalTokens = totalTokens.Add(val.Tokens)
		totalVotingPower = totalVotingPower.Add(votingPower)

		validatorDetails = append(validatorDetails, &types.EpochValidatorDetail{
			ValidatorAddress:       consAddr,
			OperatorAddress:        operatorAddr,
			BlocksSigned:           blocksSigned,
			BlocksMissed:           blocksMissed,
			BondedTokens:           val.Tokens,
			Status:                 status,
			IsSlashed:              isSlashed,
			IsJailed:               isJailed,
			AssetWeights:           convertAssetWeights(val.TotalAssetWeights),
			MissedBlockCount:       missedBlockCount,
			SigningInfoStartHeight: startHeightStr,
			CommissionRate:         commissionRate,
			VotingPower:            votingPower,
		})
	}

	return &types.QueryEpochCompleteResponse{
		Epoch:                targetEpoch,
		EpochLength:          epochLength,
		StartHeight:          startHeight,
		EndHeight:            endHeight,
		CurrentHeight:        currentHeight,
		BlocksValidated:      blocksValidated,
		BlocksRemaining:      blocksRemaining,
		BlocksUntilNextEpoch: blocksUntilNext,
		Validators:           validatorDetails,
		TotalTokens:          totalTokens,
		TotalVotingPower:     totalVotingPower,
	}, nil
}
