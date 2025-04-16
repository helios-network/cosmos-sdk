package types

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Validate performs a basic validation of the coin metadata fields. It checks:
//   - Name and Symbol are not blank
//   - Base and Display denominations are valid coin denominations
//   - Base and Display denominations are present in the DenomUnit slice
//   - Base denomination has exponent 0
//   - Denomination units are sorted in ascending order
//   - Denomination units not duplicated
func (m Metadata) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name field cannot be blank")
	}

	if strings.TrimSpace(m.Symbol) == "" {
		return errors.New("symbol field cannot be blank")
	}

	if err := sdk.ValidateDenom(m.Base); err != nil {
		return fmt.Errorf("invalid metadata base denom: %w", err)
	}

	if err := sdk.ValidateDenom(m.Display); err != nil {
		return fmt.Errorf("invalid metadata display denom: %w", err)
	}

	if err := m.ValidateLogoHash(); err != nil {
		return fmt.Errorf("invalid metadata: %w", err)
	}

	var (
		hasDisplay      bool
		currentExponent uint32 // check that the exponents are increasing
	)

	seenUnits := make(map[string]bool)

	for i, denomUnit := range m.DenomUnits {
		// The first denomination unit MUST be the base
		if i == 0 {
			// validate denomination and exponent
			if denomUnit.Denom != m.Base {
				return fmt.Errorf("metadata's first denomination unit must be the one with base denom '%s'", m.Base)
			}
			if denomUnit.Exponent != 0 {
				return fmt.Errorf("the exponent for base denomination unit %s must be 0", m.Base)
			}
		} else if currentExponent >= denomUnit.Exponent {
			return errors.New("denom units should be sorted asc by exponent")
		}

		currentExponent = denomUnit.Exponent

		if seenUnits[denomUnit.Denom] {
			return fmt.Errorf("duplicate denomination unit %s", denomUnit.Denom)
		}

		if denomUnit.Denom == m.Display {
			hasDisplay = true
		}

		if err := denomUnit.Validate(); err != nil {
			return err
		}

		seenUnits[denomUnit.Denom] = true
	}

	if !hasDisplay {
		return fmt.Errorf("metadata must contain a denomination unit with display denom '%s'", m.Display)
	}

	return nil
}

// Validate performs a basic validation of the denomination unit fields
func (du DenomUnit) Validate() error {
	if err := sdk.ValidateDenom(du.Denom); err != nil {
		return fmt.Errorf("invalid denom unit: %w", err)
	}

	seenAliases := make(map[string]bool)
	for _, alias := range du.Aliases {
		if seenAliases[alias] {
			return fmt.Errorf("duplicate denomination unit alias %s", alias)
		}

		if strings.TrimSpace(alias) == "" {
			return fmt.Errorf("alias for denom unit %s cannot be blank", du.Denom)
		}

		seenAliases[alias] = true
	}

	return nil
}

func (m Metadata) ValidateLogoHash() error {

	if m.Logo == "" {
		return nil
	}
	// 1. Check the Size sha256 e54d6dfe01574d30b0a8d7a8f83d7da95e90dcee636f7a8342c0d28cad92a8e3
	if len(m.Logo) != 64 { // 64 is the length of the sha256 hash
		return errors.New("logo is not a valid sha256 hash")
	}

	// 2. Check the logo hash is correct
	// check the m.logo is well an hex string
	if _, err := hex.DecodeString(m.Logo); err != nil {
		return errors.New("logo is not a valid hex string")
	}

	return nil
}
