package v1

import (
	"encoding/json"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
)

// NewLegacyContent creates a new MsgExecLegacyContent from a legacy Content
// interface.
func NewLegacyContent(content v1beta1.Content, authority string) (*MsgExecLegacyContent, error) {
	msg, ok := content.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("%T does not implement proto.Message", content)
	}

	any, err := codectypes.NewAnyWithValue(msg)
	if err != nil {
		return nil, err
	}

	return NewMsgExecLegacyContent(any, authority), nil
}

func NewLegacyContentFromProto(protoMsg proto.Message, authority string) (*MsgExecLegacyContent, error) {
	any, err := codectypes.NewAnyWithValue(protoMsg)
	if err != nil {
		return nil, err
	}
	return NewMsgExecLegacyContent(any, authority), nil
}

func StringToLegacyContent(cdc codec.Codec, content string, authority string) (*MsgExecLegacyContent, error) {
	var msg v1beta1.Content
	if err := json.Unmarshal([]byte(content), &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal content: %w", err)
	}
	return NewLegacyContent(msg, authority)
}

// LegacyContentFromMessage extracts the legacy Content interface from a
// MsgExecLegacyContent.
func LegacyContentFromMessage(msg *MsgExecLegacyContent) (v1beta1.Content, error) {
	content, ok := msg.Content.GetCachedValue().(v1beta1.Content)
	if !ok {
		return nil, sdkerrors.ErrInvalidType.Wrapf("expected %T, got %T", (*v1beta1.Content)(nil), msg.Content.GetCachedValue())
	}

	return content, nil
}
