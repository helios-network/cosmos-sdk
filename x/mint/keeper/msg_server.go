package keeper

import (
	"context"

	"cosmossdk.io/errors"

	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

var _ types.MsgServer = msgServer{}

// msgServer is a wrapper of Keeper.
type msgServer struct {
	Keeper
	authority string
}

// NewMsgServerImpl returns an implementation of the x/mint MsgServer interface.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return &msgServer{
		Keeper:    k,
		authority: k.GetAuthority(),
	}
}

// UpdateParams updates the params.
func (ms msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if ms.authority != msg.Authority {
		return nil, errors.Wrapf(govtypes.ErrInvalidSigner, "invalid authority; expected %s, got %s", ms.authority, msg.Authority)
	}

	if err := msg.Params.Validate(); err != nil {
		return nil, err
	}

	if err := ms.Params.Set(ctx, msg.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}

// NOT CALLABLE BUT HERE IN CASE AN UPGRADE IS REQUIRED ON CHAIN BY URGENCY
// UpdateInflationRate updates the inflation rate for a specific phase
func (ms msgServer) UpdateInflationRate(goCtx context.Context, msg *types.MsgUpdateInflationRate) (*types.MsgUpdateInflationRateResponse, error) {
	if ms.authority != msg.Authority {
		return nil, errors.Wrapf(govtypes.ErrInvalidSigner, "invalid authority; expected %s, got %s", ms.authority, msg.Authority)
	}

	// Create an UpdateInflationProposal from the message
	proposal := &types.UpdateInflationProposal{
		Title:       "Direct inflation rate update",      // Title for logging purposes
		Description: "Update via MsgUpdateInflationRate", // Description for logging
		Phase:       msg.Phase,
		NewRate:     msg.NewRate,
	}

	// Use the existing handler function
	if err := ms.HandleUpdateInflationProposal(goCtx, proposal); err != nil {
		return nil, err
	}

	return &types.MsgUpdateInflationRateResponse{}, nil
}
