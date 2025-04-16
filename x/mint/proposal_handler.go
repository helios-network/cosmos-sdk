// x/mint/proposal_handler.go
package mint

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"

	"github.com/cosmos/cosmos-sdk/x/mint/keeper"
)

// NewProposalHandler creates a governance handler for mint module proposals
func NewProposalHandler(k keeper.Keeper) govtypes.Handler {
	return func(ctx sdk.Context, content govtypes.Content) error {
		return k.HandleUpdateInflationRateProposal(ctx, content)
	}
}
