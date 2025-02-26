package types

import (
	"fmt"

	"cosmossdk.io/math"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
)

const (
	RouterKey = ModuleName // This should be your module name
)

// ProposalRoute returns the routing key for this proposal
func (p *UpdateInflationProposal) ProposalRoute() string { return RouterKey }

// ProposalType returns the type of this proposal
func (p *UpdateInflationProposal) ProposalType() string { return ProposalTypeUpdateInflationRate }

// ValidateBasic runs basic stateless validity checks
func (p *UpdateInflationProposal) ValidateBasic() error {
	err := govtypes.ValidateAbstract(p)
	if err != nil {
		return err
	}

	// Validate phase
	validPhases := map[string]bool{
		"early":    true,
		"growth":   true,
		"mature":   true,
		"post-cap": true,
	}
	if !validPhases[p.Phase] {
		return fmt.Errorf("invalid phase: %s, must be one of: early, growth, mature, post-cap", p.Phase)
	}

	// Validate rate is between 0 and 100%
	if p.NewRate.IsNegative() || p.NewRate.GT(math.LegacyNewDecWithPrec(100, 2)) {
		return fmt.Errorf("inflation rate must be between 0%% and 100%%")
	}

	return nil
}

func init() {
	govtypes.RegisterProposalType(ProposalTypeUpdateInflationRate)
}
