package types

import (
	"strings"

	errorsmod "cosmossdk.io/errors"
	v1beta1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
)

// constants
const (
	ProposalSlashing string = "SlashingProposal"
)

// Implements Proposal Interface
var (
	_ v1beta1.Content = &SlashingProposal{}
)

func init() {
	v1beta1.RegisterProposalType(ProposalSlashing)
}

/////////////////////////////////////////////////////////
// AddCounterpartyChainParamsProposal
/////////////////////////////////////////////////////////

func NewSlashingProposal(title, description, msg string) v1beta1.Content {
	return &SlashingProposal{
		Title:       title,
		Description: description,
		Msg:         msg,
	}
}

// ProposalRoute returns router key for this proposal
func (*SlashingProposal) ProposalRoute() string { return RouterKey }

// ProposalType returns proposal type for this proposal
func (*SlashingProposal) ProposalType() string {
	return ProposalSlashing
}

// ValidateBasic performs a stateless check of the proposal fields
func (p *SlashingProposal) ValidateBasic() error {
	// Validate title
	if strings.TrimSpace(p.Title) == "" {
		return errorsmod.Wrap(v1beta1.ErrInvalidLengthQuery, "proposal title cannot be empty")
	}

	// Validate description
	if strings.TrimSpace(p.Description) == "" {
		return errorsmod.Wrap(v1beta1.ErrInvalidLengthQuery, "proposal description cannot be empty")
	}

	return nil
}

// GetDescription returns the description of the proposal.
func (p *SlashingProposal) GetDescription() string {
	return p.Description
}

// GetTitle returns the title of the proposal.
func (p *SlashingProposal) GetTitle() string {
	return p.Title
}

/////////////////////////////////////////////////////////
