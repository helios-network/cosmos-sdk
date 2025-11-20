package types

import (
	"cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
)

var _ sdk.Msg = &MsgUpdateInflationRate{}

// ValidateBasic does a sanity check on the provided data.
func (msg *MsgUpdateInflationRate) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Authority); err != nil {
		return errors.Wrap(err, "invalid authority address")
	}

	if msg.NewRate.IsNil() {
		return errors.Wrap(govtypes.ErrInvalidProposalContent, "new rate cannot be nil")
	}

	if msg.NewRate.IsNegative() {
		return errors.Wrap(govtypes.ErrInvalidProposalContent, "new rate cannot be negative")
	}

	if msg.Phase == "" {
		return errors.Wrap(govtypes.ErrInvalidProposalContent, "phase cannot be empty")
	}

	return nil
}

// GetSigners returns the signers of the message.
func (msg *MsgUpdateInflationRate) GetSigners() []sdk.AccAddress {
	addr, _ := sdk.AccAddressFromBech32(msg.Authority)
	return []sdk.AccAddress{addr}
}

