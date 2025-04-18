package types

import (
	"cosmossdk.io/core/address"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

var (
	_ sdk.Msg                            = &MsgCreateValidator{}
	_ codectypes.UnpackInterfacesMessage = (*MsgCreateValidator)(nil)
	_ sdk.Msg                            = &MsgEditValidator{}
	_ sdk.Msg                            = &MsgDelegate{}
	_ sdk.Msg                            = &MsgUndelegate{}
	_ sdk.Msg                            = &MsgBeginRedelegate{}
	_ sdk.Msg                            = &MsgCancelUnbondingDelegation{}
	_ sdk.Msg                            = &MsgUpdateParams{}
)

// NewMsgCreateValidator creates a new MsgCreateValidator instance.
// Delegator address and validator address are the same.
func NewMsgCreateValidator(
	valAddr string, pubKey cryptotypes.PubKey,
	selfDelegation sdk.Coin, description Description, commission CommissionRates, minSelfDelegation math.Int,
) (*MsgCreateValidator, error) {
	var pkAny *codectypes.Any
	if pubKey != nil {
		var err error
		if pkAny, err = codectypes.NewAnyWithValue(pubKey); err != nil {
			return nil, err
		}
	}
	return &MsgCreateValidator{
		Description:       description,
		ValidatorAddress:  valAddr,
		Pubkey:            pkAny,
		Value:             selfDelegation,
		Commission:        commission,
		MinSelfDelegation: minSelfDelegation,
	}, nil
}

// Validate validates the MsgCreateValidator sdk msg.
func (msg MsgCreateValidator) Validate(ac address.Codec) error {
	// note that unmarshaling from bech32 ensures both non-empty and valid
	_, err := ac.StringToBytes(msg.ValidatorAddress)
	if err != nil {
		return sdkerrors.ErrInvalidAddress.Wrapf("invalid validator address: %s", err)
	}

	if msg.Pubkey == nil {
		return ErrEmptyValidatorPubKey
	}

	if !msg.Value.IsValid() || !msg.Value.Amount.IsPositive() {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "invalid delegation amount")
	}

	if msg.Description == (Description{}) {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "empty description")
	}

	if msg.Commission == (CommissionRates{}) {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "empty commission")
	}

	if err := msg.Commission.Validate(); err != nil {
		return err
	}

	if !msg.MinSelfDelegation.IsPositive() {
		return errorsmod.Wrap(
			sdkerrors.ErrInvalidRequest,
			"minimum self delegation must be a positive integer",
		)
	}

	if msg.Value.Amount.LT(msg.MinSelfDelegation) {
		return ErrSelfDelegationBelowMinimum
	}

	if msg.MinDelegation.LT(math.ZeroInt()) {
		return errorsmod.Wrap(
			sdkerrors.ErrInvalidRequest,
			"minimum delegation must be a positive integer",
		)
	}

	return nil
}

// UnpackInterfaces implements UnpackInterfacesMessage.UnpackInterfaces
func (msg MsgCreateValidator) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	var pubKey cryptotypes.PubKey
	return unpacker.UnpackAny(msg.Pubkey, &pubKey)
}

// NewMsgEditValidator creates a new MsgEditValidator instance
func NewMsgEditValidator(valAddr string, description Description, newRate *math.LegacyDec, newMinSelfDelegation *math.Int) *MsgEditValidator {
	return &MsgEditValidator{
		Description:       description,
		CommissionRate:    newRate,
		ValidatorAddress:  valAddr,
		MinSelfDelegation: newMinSelfDelegation,
	}
}

// ValidateBasic implements the sdk.Msg interface.
func (msg MsgEditValidator) ValidateBasic() error {
	if _, err := sdk.ValAddressFromBech32(msg.ValidatorAddress); err != nil {
		return sdkerrors.ErrInvalidAddress.Wrapf("invalid validator address: %s", err)
	}

	if msg.Description == (Description{}) {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "empty description")
	}

	if msg.MinSelfDelegation != nil && !msg.MinSelfDelegation.IsPositive() {
		return errorsmod.Wrap(
			sdkerrors.ErrInvalidRequest,
			"minimum self delegation must be a positive integer",
		)
	}

	if msg.CommissionRate != nil {
		if msg.CommissionRate.GT(math.LegacyOneDec()) || msg.CommissionRate.IsNegative() {
			return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "commission rate must be between 0 and 1 (inclusive)")
		}
	}

	return nil
}

// NewMsgDelegate creates a new MsgDelegate instance.
func NewMsgDelegate(delAddr, valAddr string, amount sdk.Coin) *MsgDelegate {
	return &MsgDelegate{
		DelegatorAddress: delAddr,
		ValidatorAddress: valAddr,
		Amount:           amount,
	}
}

// NewMsgBeginRedelegate creates a new MsgBeginRedelegate instance.
func NewMsgBeginRedelegate(
	delAddr, valSrcAddr, valDstAddr string, amount sdk.Coin,
) *MsgBeginRedelegate {
	return &MsgBeginRedelegate{
		DelegatorAddress:    delAddr,
		ValidatorSrcAddress: valSrcAddr,
		ValidatorDstAddress: valDstAddr,
		Amount:              amount,
	}
}

// NewMsgUndelegate creates a new MsgUndelegate instance.
func NewMsgUndelegate(delAddr, valAddr string, amount sdk.Coin) *MsgUndelegate {
	return &MsgUndelegate{
		DelegatorAddress: delAddr,
		ValidatorAddress: valAddr,
		Amount:           amount,
	}
}

// NewMsgCancelUnbondingDelegation creates a new MsgCancelUnbondingDelegation instance.
func NewMsgCancelUnbondingDelegation(delAddr, valAddr string, creationHeight int64, amount sdk.Coin) *MsgCancelUnbondingDelegation {
	return &MsgCancelUnbondingDelegation{
		DelegatorAddress: delAddr,
		ValidatorAddress: valAddr,
		Amount:           amount,
		CreationHeight:   creationHeight,
	}
}
