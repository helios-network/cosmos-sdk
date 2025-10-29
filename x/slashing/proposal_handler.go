package slashing

import (
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
	"github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	"github.com/cosmos/cosmos-sdk/x/slashing/types"
)

// NewSlashingProposalHandler creates a governance handler to manage all slashing proposal types.
func NewSlashingProposalHandler(k keeper.Keeper) govtypes.Handler {
	return func(ctx sdk.Context, content govtypes.Content) error {
		switch c := content.(type) {
		case *types.SlashingProposal:
			return HandleSlashingProposal(ctx, k, c)
		default:
			return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "unrecognized slashing proposal content type: %T", c)
		}
	}
}

func HandleSlashingProposal(ctx sdk.Context, k keeper.Keeper, proposal *types.SlashingProposal) error {
	// Validate the proposal
	if err := proposal.ValidateBasic(); err != nil {
		return err
	}

	var msg sdk.Msg

	if err := k.Cdc().UnmarshalJSON([]byte(proposal.Msg), &msg); err != nil {
		return err
	}

	switch msg := msg.(type) {
	case *types.MsgUpdateParams:
		msg.Authority = k.GetAuthority()
		_, err := keeper.NewMsgServerImpl(k).UpdateParams(ctx, msg)
		if err != nil {
			return err
		}
	default:
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "unrecognized slashing proposal message type: %T", msg)
	}
	return nil
}
