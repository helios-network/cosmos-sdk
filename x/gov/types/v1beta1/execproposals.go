package v1beta1

import (
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

var _ Content = (*ModuleExecProposal)(nil)
var _ codectypes.UnpackInterfacesMessage = (*ModuleExecProposal)(nil)

// Implements Proposal Interface
var (
	_ Content = &ModuleExecProposal{}
)

/////////////////////////////////////////////////////////
// AddCounterpartyChainParamsProposal
/////////////////////////////////////////////////////////

func NewModuleExecProposal(title, description, route string, messages []sdk.Msg) Content {
	anys, _ := sdktx.SetMsgs(messages)
	return &ModuleExecProposal{
		Title:       title,
		Description: description,
		Route:       route,
		Messages:    anys,
	}
}

func (p *ModuleExecProposal) ProposalRoute() string { return p.Route }
func (p *ModuleExecProposal) ProposalType() string  { return "ModuleExecProposal" }
func (p *ModuleExecProposal) ValidateBasic() error {
	if p.Title == "" || p.Description == "" {
		return fmt.Errorf("empty title/description")
	}
	if p.Route == "" {
		return fmt.Errorf("empty route")
	}
	if len(p.Messages) == 0 {
		return fmt.Errorf("no messages")
	}
	return nil
}
func (p *ModuleExecProposal) UnpackInterfaces(ur codectypes.AnyUnpacker) error {
	for _, a := range p.Messages {
		var m sdk.Msg
		if err := ur.UnpackAny(a, &m); err != nil {
			return err
		}
	}
	return nil
}
