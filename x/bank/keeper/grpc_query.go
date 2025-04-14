package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	"cosmossdk.io/store/prefix"

	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/x/bank/types"
)

type Querier struct {
	BaseKeeper
}

var _ types.QueryServer = BaseKeeper{}

func NewQuerier(keeper *BaseKeeper) Querier {
	return Querier{BaseKeeper: *keeper}
}

// Balance implements the Query/Balance gRPC method
func (k BaseKeeper) Balance(ctx context.Context, req *types.QueryBalanceRequest) (*types.QueryBalanceResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	address, err := k.ak.AddressCodec().StringToBytes(req.Address)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid address: %s", err.Error())
	}

	balance := k.GetBalance(sdkCtx, address, req.Denom)

	return &types.QueryBalanceResponse{Balance: &balance}, nil
}

// AllBalances implements the Query/AllBalances gRPC method
func (k BaseKeeper) AllBalances(ctx context.Context, req *types.QueryAllBalancesRequest) (*types.QueryAllBalancesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	addr, err := k.ak.AddressCodec().StringToBytes(req.Address)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid address: %s", err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	balances := []sdk.Coin{}

	_, pageRes, err := k.BaseViewKeeper.iterateBalancesByHoldersCountForAddress(
		ctx,
		addr,
		req.Pagination,
		func(address sdk.AccAddress, coin sdk.Coin, holdersCount uint64) bool {
			if req.ResolveDenom {
				if metadata, ok := k.GetDenomMetaData(sdkCtx, coin.Denom); ok {
					balances = append(balances, sdk.NewCoin(metadata.Display, coin.Amount))
					return true
				}
			}
			if req.ResolveSymbol {
				if metadata, ok := k.GetDenomMetaData(sdkCtx, coin.Denom); ok {
					balances = append(balances, sdk.NewCoin(metadata.Symbol, coin.Amount))
					return true
				}
			}
			balances = append(balances, coin)
			return true
		},
	)

	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "paginate: %v", err)
	}

	return &types.QueryAllBalancesResponse{Balances: balances, Pagination: pageRes}, nil
}

func (k BaseKeeper) AllBalancesWithFullMetadata(ctx context.Context, req *types.QueryAllBalancesWithFullMetadataRequest) (*types.QueryAllBalancesWithFullMetadataResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	addr, err := k.ak.AddressCodec().StringToBytes(req.Address)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid address: %s", err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	balances := []*types.TokenBalanceWithFullMetadata{}

	_, pageRes, err := k.BaseViewKeeper.iterateBalancesByHoldersCountForAddress(
		ctx,
		addr,
		req.Pagination,
		func(address sdk.AccAddress, coin sdk.Coin, holdersCount uint64) bool {

			fullMetadata, err := k.DenomFullMetadata(sdkCtx, &types.QueryDenomFullMetadataRequest{
				Denom: coin.Denom,
			})
			if err != nil {
				return false
			}
			balances = append(balances, &types.TokenBalanceWithFullMetadata{
				FullMetadata: &fullMetadata.Metadata,
				Balance:      coin.Amount,
			})
			return true
		},
	)

	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "paginate: %v", err)
	}

	totalCount, _, _ := k.BaseViewKeeper.iterateBalancesByHoldersCountForAddress(
		ctx,
		addr,
		&query.PageRequest{
			Offset: 0,
			Limit:  pageRes.Total,
		},
		func(address sdk.AccAddress, coin sdk.Coin, holdersCount uint64) bool {

			fullMetadata, err := k.DenomFullMetadata(sdkCtx, &types.QueryDenomFullMetadataRequest{
				Denom: coin.Denom,
			})
			if err != nil {
				return false
			}
			balances = append(balances, &types.TokenBalanceWithFullMetadata{
				FullMetadata: &fullMetadata.Metadata,
				Balance:      coin.Amount,
			})
			return true
		},
	)

	return &types.QueryAllBalancesWithFullMetadataResponse{Balances: balances, Pagination: pageRes, TotalCount: totalCount}, nil
}

// SpendableBalances implements a gRPC query handler for retrieving an account's
// spendable balances.
func (k BaseKeeper) SpendableBalances(ctx context.Context, req *types.QuerySpendableBalancesRequest) (*types.QuerySpendableBalancesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	addr, err := k.ak.AddressCodec().StringToBytes(req.Address)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid address: %s", err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	zeroAmt := math.ZeroInt()

	balances, pageRes, err := query.CollectionPaginate(ctx, k.Balances, req.Pagination, func(key collections.Pair[sdk.AccAddress, string], _ math.Int) (coin sdk.Coin, err error) {
		return sdk.NewCoin(key.K2(), zeroAmt), nil
	}, query.WithCollectionPaginationPairPrefix[sdk.AccAddress, string](addr))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "paginate: %v", err)
	}

	result := sdk.NewCoins()
	spendable := k.SpendableCoins(sdkCtx, addr)

	for _, c := range balances {
		result = append(result, sdk.NewCoin(c.Denom, spendable.AmountOf(c.Denom)))
	}

	return &types.QuerySpendableBalancesResponse{Balances: result, Pagination: pageRes}, nil
}

// SpendableBalanceByDenom implements a gRPC query handler for retrieving an account's
// spendable balance for a specific denom.
func (k BaseKeeper) SpendableBalanceByDenom(ctx context.Context, req *types.QuerySpendableBalanceByDenomRequest) (*types.QuerySpendableBalanceByDenomResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	addr, err := k.ak.AddressCodec().StringToBytes(req.Address)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid address: %s", err.Error())
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	spendable := k.SpendableCoin(sdkCtx, addr, req.Denom)

	return &types.QuerySpendableBalanceByDenomResponse{Balance: &spendable}, nil
}

// TotalSupply implements the Query/TotalSupply gRPC method
func (k BaseKeeper) TotalSupply(ctx context.Context, req *types.QueryTotalSupplyRequest) (*types.QueryTotalSupplyResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	totalSupply, pageRes, err := k.GetPaginatedTotalSupply(sdkCtx, req.Pagination)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryTotalSupplyResponse{Supply: totalSupply, Pagination: pageRes}, nil
}

// SupplyOf implements the Query/SupplyOf gRPC method
func (k BaseKeeper) SupplyOf(c context.Context, req *types.QuerySupplyOfRequest) (*types.QuerySupplyOfResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	ctx := sdk.UnwrapSDKContext(c)
	supply := k.GetSupply(ctx, req.Denom)

	return &types.QuerySupplyOfResponse{Amount: sdk.NewCoin(req.Denom, supply.Amount)}, nil
}

// Params implements the gRPC service handler for querying x/bank parameters.
func (k BaseKeeper) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	params := k.GetParams(sdkCtx)

	return &types.QueryParamsResponse{Params: params}, nil
}

// DenomsMetadata implements Query/DenomsMetadata gRPC method.
func (k BaseKeeper) DenomsMetadata(c context.Context, req *types.QueryDenomsMetadataRequest) (*types.QueryDenomsMetadataResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}
	kvStore := runtime.KVStoreAdapter(k.storeService.OpenKVStore(c))
	store := prefix.NewStore(kvStore, types.DenomMetadataPrefix)

	metadatas := []types.Metadata{}
	pageRes, err := query.Paginate(store, req.Pagination, func(_, value []byte) error {
		var metadata types.Metadata
		k.cdc.MustUnmarshal(value, &metadata)

		metadatas = append(metadatas, metadata)
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryDenomsMetadataResponse{
		Metadatas:  metadatas,
		Pagination: pageRes,
	}, nil
}

// DenomMetadata implements Query/DenomMetadata gRPC method.
func (k BaseKeeper) DenomMetadata(c context.Context, req *types.QueryDenomMetadataRequest) (*types.QueryDenomMetadataResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	ctx := sdk.UnwrapSDKContext(c)

	metadata, found := k.GetDenomMetaData(ctx, req.Denom)
	if !found {
		return nil, status.Errorf(codes.NotFound, "client metadata for denom %s", req.Denom)
	}

	return &types.QueryDenomMetadataResponse{
		Metadata: metadata,
	}, nil
}

// DenomMetadataByQueryString is identical to DenomMetadata query, but receives request via query string.
func (k BaseKeeper) DenomMetadataByQueryString(c context.Context, req *types.QueryDenomMetadataByQueryStringRequest) (*types.QueryDenomMetadataByQueryStringResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	res, err := k.DenomMetadata(c, &types.QueryDenomMetadataRequest{
		Denom: req.Denom,
	})
	if err != nil {
		return nil, err
	}

	return &types.QueryDenomMetadataByQueryStringResponse{Metadata: res.Metadata}, nil
}

// DenomsFullMetadata implements Query/DenomsFullMetadata gRPC method.
func (k BaseKeeper) DenomsFullMetadata(c context.Context, req *types.QueryDenomsFullMetadataRequest) (*types.QueryDenomsFullMetadataResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	metadataList, pageRes, err := query.CollectionPaginate(
		c,
		k.HoldersSortedIndex,
		req.Pagination,
		func(key collections.Pair[uint64, string], _ bool) (types.FullMetadata, error) {
			denom := key.K2()
			metadata, _ := k.GetDenomMetaData(c, denom)
			holdersCount := ^key.K1()

			return types.FullMetadata{
				Metadata:     &metadata,
				TotalSupply:  k.GetSupply(c, metadata.Base).Amount,
				HoldersCount: holdersCount,
			}, nil
		},
	)

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to paginate: %v", err)
	}

	return &types.QueryDenomsFullMetadataResponse{
		Metadatas:  metadataList,
		Pagination: pageRes,
	}, nil
}

// DenomFullMetadata implements Query/DenomFullMetadata gRPC method.
func (k BaseKeeper) DenomFullMetadata(c context.Context, req *types.QueryDenomFullMetadataRequest) (*types.QueryDenomFullMetadataResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	ctx := sdk.UnwrapSDKContext(c)

	metadata, found := k.GetDenomMetaData(ctx, req.Denom)
	if !found {
		return nil, status.Errorf(codes.NotFound, "client metadata for denom %s", req.Denom)
	}

	holdersCount, _ := k.HoldersCount.Get(c, metadata.Base)

	return &types.QueryDenomFullMetadataResponse{
		Metadata: types.FullMetadata{
			Metadata:     &metadata,
			TotalSupply:  k.GetSupply(c, metadata.Base).Amount,
			HoldersCount: holdersCount,
		},
	}, nil
}

// DenomFullMetadataByQueryString is identical to DenomFullMetadata query, but receives request via query string.
func (k BaseKeeper) DenomFullMetadataByQueryString(c context.Context, req *types.QueryDenomFullMetadataByQueryStringRequest) (*types.QueryDenomFullMetadataByQueryStringResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	res, err := k.DenomFullMetadata(c, &types.QueryDenomFullMetadataRequest{
		Denom: req.Denom,
	})
	if err != nil {
		return nil, err
	}

	return &types.QueryDenomFullMetadataByQueryStringResponse{Metadata: res.Metadata}, nil
}

func (k BaseKeeper) DenomOwners(
	ctx context.Context,
	req *types.QueryDenomOwnersRequest,
) (*types.QueryDenomOwnersResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	denomOwners, pageRes, err := query.CollectionPaginate(
		ctx,
		k.Balances.Indexes.Denom,
		req.Pagination,
		func(key collections.Pair[string, sdk.AccAddress], value collections.NoValue) (*types.DenomOwner, error) {
			amt, err := k.Balances.Get(ctx, collections.Join(key.K2(), req.Denom))
			if err != nil {
				return nil, err
			}
			return &types.DenomOwner{Address: key.K2().String(), Balance: sdk.NewCoin(req.Denom, amt)}, nil
		},
		query.WithCollectionPaginationPairPrefix[string, sdk.AccAddress](req.Denom),
	)
	if err != nil {
		return nil, err
	}

	return &types.QueryDenomOwnersResponse{DenomOwners: denomOwners, Pagination: pageRes}, nil
}

// DenomOwnersCount returns the total number of addresses holding a specific denomination
func (k BaseKeeper) DenomOwnersCount(ctx context.Context, req *types.QueryDenomOwnersCountRequest) (*types.QueryDenomOwnersCountResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if err := sdk.ValidateDenom(req.Denom); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	count, err := k.HoldersCount.Get(ctx, req.Denom)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to count denom owners: %v", err)
	}

	return &types.QueryDenomOwnersCountResponse{Count: count}, nil
}

func (k BaseKeeper) SendEnabled(goCtx context.Context, req *types.QuerySendEnabledRequest) (*types.QuerySendEnabledResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	resp := &types.QuerySendEnabledResponse{}
	if len(req.Denoms) > 0 {
		for _, denom := range req.Denoms {
			if se, ok := k.getSendEnabled(ctx, denom); ok {
				resp.SendEnabled = append(resp.SendEnabled, types.NewSendEnabled(denom, se))
			}
		}
	} else {
		results, pageResp, err := query.CollectionPaginate(
			ctx,
			k.BaseViewKeeper.SendEnabled,
			req.Pagination, func(key string, value bool) (*types.SendEnabled, error) {
				return types.NewSendEnabled(key, value), nil
			},
		)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		resp.SendEnabled = results
		resp.Pagination = pageResp
	}

	return resp, nil
}

// DenomOwnersByQuery is identical to DenomOwner query, but receives denom values via query string.
func (k BaseKeeper) DenomOwnersByQuery(ctx context.Context, req *types.QueryDenomOwnersByQueryRequest) (*types.QueryDenomOwnersByQueryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}
	resp, err := k.DenomOwners(ctx, &types.QueryDenomOwnersRequest{
		Denom:      req.Denom,
		Pagination: req.Pagination,
	})
	if err != nil {
		return nil, err
	}

	return &types.QueryDenomOwnersByQueryResponse{DenomOwners: resp.DenomOwners, Pagination: resp.Pagination}, nil
}

func (k BaseKeeper) DenomsByChainId(c context.Context, req *types.QueryDenomsByChainIdRequest) (*types.QueryDenomsByChainIdResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	if req.ChainId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "chain_id cannot be empty")
	}

	ctx := sdk.UnwrapSDKContext(c)

	// Si l'option de tri par nombre de détenteurs est activée, utiliser l'index optimisé
	if req.OrderByHoldersCount {
		// Utiliser ChainHoldersIndex qui est déjà trié par nombre de détenteurs pour chaque chainId
		denomsList, pageRes, err := query.CollectionPaginate(
			ctx,
			k.ChainHoldersIndex,
			req.Pagination,
			func(key collections.Triple[uint64, uint64, string], _ bool) (types.FullMetadata, error) {
				// Vérifier que le chainId correspond
				if key.K1() != req.ChainId {
					return types.FullMetadata{}, nil
				}

				denom := key.K3()
				holdersCount := ^key.K2() // Inverser pour retrouver le vrai nombre

				// Récupérer les métadonnées complètes
				metadata, found := k.GetDenomMetaData(ctx, denom)
				if !found {
					return types.FullMetadata{}, nil
				}

				return types.FullMetadata{
					Metadata:     &metadata,
					TotalSupply:  k.GetSupply(c, denom).Amount,
					HoldersCount: holdersCount,
				}, nil
			},
			query.WithCollectionPaginationTriplePrefix[uint64, uint64, string](req.ChainId),
		)

		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to paginate: %v", err)
		}

		return &types.QueryDenomsByChainIdResponse{
			Metadatas:  denomsList,
			Pagination: pageRes,
		}, nil
	}

	// Retrieve the denoms indexed by chainId in a paginated way
	denomsList, pageRes, err := query.CollectionPaginate(
		ctx,
		k.OriginChainIndex,
		req.Pagination,
		func(key collections.Pair[uint64, string], value string) (types.FullMetadata, error) {
			// Check if the first part of the key corresponds to the requested chainId
			if key.K1() != req.ChainId {
				return types.FullMetadata{}, nil
			}

			// Retrieve the full metadata for this denom
			metadata, found := k.GetDenomMetaData(ctx, value)
			if !found {
				return types.FullMetadata{}, nil
			}

			// Retrieve the number of holders and provide the full metadata
			holdersCount, _ := k.HoldersCount.Get(c, value)

			return types.FullMetadata{
				Metadata:     &metadata,
				TotalSupply:  k.GetSupply(c, value).Amount,
				HoldersCount: holdersCount,
			}, nil
		},
		query.WithCollectionPaginationPairPrefix[uint64, string](req.ChainId),
	)

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to paginate: %v", err)
	}

	return &types.QueryDenomsByChainIdResponse{
		Metadatas:  denomsList,
		Pagination: pageRes,
	}, nil
}
