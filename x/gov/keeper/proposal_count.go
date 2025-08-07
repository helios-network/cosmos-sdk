package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

// TODO: REMOVE AFTER HARD RESET
// ProposalsCountActivationHeight defines the block height at which the optimized proposal count system is activated
const ProposalsCountActivationHeight = 150000

// GetProposalsCount returns the current count of existing proposals
func (k Keeper) GetProposalsCount(ctx context.Context) (uint64, error) {
	count, err := k.ProposalsCount.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}

// IncrementProposalsCount increments the proposals count by 1 atomically
func (k Keeper) IncrementProposalsCount(ctx context.Context) error {
	currentCount, err := k.ProposalsCount.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return k.ProposalsCount.Set(ctx, 1)
		}
		return err
	}
	return k.ProposalsCount.Set(ctx, currentCount+1)
}

// DecrementProposalsCount decrements the proposals count by 1 atomically
func (k Keeper) DecrementProposalsCount(ctx context.Context) error {
	currentCount, err := k.ProposalsCount.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}

	if currentCount > 0 {
		return k.ProposalsCount.Set(ctx, currentCount-1)
	}

	return nil
}

// GetProposalsCountActivationHeight returns the activation height for the proposal count system
func (k Keeper) GetProposalsCountActivationHeight() int64 {
	return ProposalsCountActivationHeight
}

// IsProposalCountSystemActive checks if the optimized proposal count system is active based on current block height
func (k Keeper) IsProposalCountSystemActive(ctx context.Context) bool {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.BlockHeight() >= ProposalsCountActivationHeight
}

// InitializeProposalsCount performs the initial calculation of proposals count when the system is first activated
func (k Keeper) InitializeProposalsCount(ctx context.Context) error {
	count := uint64(0)
	err := k.Proposals.Walk(ctx, nil, func(key uint64, value v1.Proposal) (bool, error) {
		count++
		return false, nil
	})
	if err != nil {
		return err
	}
	return k.ProposalsCount.Set(ctx, count)
}
