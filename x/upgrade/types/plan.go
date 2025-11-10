package types

import (
	"encoding/json"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

// UpgradeInfoFileName file to store upgrade information
const UpgradeInfoFilename = "upgrade-info.json"

type PlanInfo struct {
	Version string `json:"version"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
}

// ValidateBasic does basic validation of a Plan
func (p Plan) ValidateBasic() error {
	if !p.Time.IsZero() {
		return sdkerrors.ErrInvalidRequest.Wrap("time-based upgrades have been deprecated in the SDK")
	}
	if p.UpgradedClientState != nil {
		return sdkerrors.ErrInvalidRequest.Wrap("upgrade logic for IBC has been moved to the IBC module")
	}
	if len(p.Name) == 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "name cannot be empty")
	}
	if p.Height <= 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "height must be greater than 0")
	}
	if len(p.Info) == 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "info cannot be empty")
	}
	// check if the info is a valid json
	var info PlanInfo
	err := json.Unmarshal([]byte(p.Info), &info)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "info is not a valid json")
	}
	if info.Version == "" {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "version cannot be empty")
	}
	if info.Hash == "" {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "hash cannot be empty")
	}
	if info.Size <= 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "size must be greater than 0")
	}
	return nil
}

// ShouldExecute returns true if the Plan is ready to execute given the current block height
func (p Plan) ShouldExecute(blockHeight int64) bool {
	return p.Height > 0 && p.Height <= blockHeight
}

// GetPlanInfo parses the Plan.Info string into a PlanInfo struct
func (p Plan) GetPlanInfo() (*PlanInfo, error) {
	var info PlanInfo
	err := json.Unmarshal([]byte(p.Info), &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// DueAt is a string representation of when this plan is due to be executed
func (p Plan) DueAt() string {
	return fmt.Sprintf("height: %d", p.Height)
}
