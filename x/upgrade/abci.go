package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/x/upgrade/keeper"
	"cosmossdk.io/x/upgrade/types"

	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// PreBlocker will check if there is a scheduled plan and if it is ready to be executed.
// If the current height is in the provided set of heights to skip, it will skip and clear the upgrade plan.
// If it is ready, it will execute it if the handler is installed, and panic/abort otherwise.
// If the plan is not ready, it will ensure the handler is not registered too early (and abort otherwise).
//
// The purpose is to ensure the binary is switched EXACTLY at the desired block, and to allow
// a migration to be executed if needed upon this switch (migration defined in the new binary)
// skipUpgradeHeightArray is a set of block heights for which the upgrade must be skipped
func PreBlocker(ctx context.Context, k *keeper.Keeper) (appmodule.ResponsePreBlock, error) {
	defer telemetry.ModuleMeasureSince(types.ModuleName, telemetry.Now(), telemetry.MetricKeyBeginBlocker)

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	blockHeight := sdkCtx.HeaderInfo().Height
	plan, err := k.GetUpgradePlan(ctx)
	if err != nil && !errors.Is(err, types.ErrNoUpgradePlanFound) {
		fmt.Println("Error getting the upgrade plan", err)
		return nil, err
	}
	found := err == nil

	if !found {
		return &sdk.ResponsePreBlock{
			ConsensusParamsChanged: false,
		}, nil
	}
	appVersion := k.GetAppVersion(ctx)
	logger := k.Logger(ctx)

	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		return nil, err
	}

	if !k.VersionIsOlderThan(appVersion, planInfo.Version) {
		fmt.Println("AppVersion (", appVersion, ") is up to date with the plan version (", planInfo.Version, "), skipping upgrade")
		return &sdk.ResponsePreBlock{
			ConsensusParamsChanged: false,
		}, nil
	}

	// skip if current version is the version of the plan and the binary is not downloaded
	if blockHeight <= plan.Height {
		if !k.VerifyIfTheBinaryHasBeenDownloadedForThePlan(plan) {
			fmt.Println("Downloading the upgrade binary and preparing it on the storage")
			// Download the upgrade binary and prepare it on the storage
			err := k.TryDownloadUpgradeBinary(ctx, plan, []string{"https://github.com/helios-network/helios-core/releases/download/"}, "heliades")
			if err != nil {
				fmt.Println("Error downloading the upgrade binary", err)
			}
		} else {
			fmt.Println("Plan binary is ready to be applied")
		}
	}

	// To make sure clear upgrade is executed at the same block
	if plan.ShouldExecute(blockHeight) {
		// If skip upgrade has been set for current height, we clear the upgrade plan
		if k.IsSkipHeight(blockHeight) {
			skipUpgradeMsg := fmt.Sprintf("UPGRADE \"%s\" SKIPPED at %d: %s", plan.Name, plan.Height, plan.Info)
			logger.Info(skipUpgradeMsg)

			// Clear the upgrade plan at current height
			if err := k.ClearUpgradePlan(ctx); err != nil {
				return nil, err
			}
			return &sdk.ResponsePreBlock{
				ConsensusParamsChanged: false,
			}, nil
		}

		// Write the upgrade info to disk. The UpgradeStoreLoader uses this info to perform or skip
		// store migrations.
		err := k.DumpUpgradeInfoToDisk(blockHeight, plan)
		if err != nil {
			return nil, fmt.Errorf("unable to write upgrade info to filesystem: %w", err)
		}

		upgradeMsg := BuildUpgradeNeededMsg(plan)
		logger.Error(upgradeMsg)

		err = k.ApplyUpgrade(ctx, plan)
		if err != nil {
			return nil, fmt.Errorf("unable to apply upgrade: %w", err)
		}

		// stop the node
		fmt.Println("Stopping the node")
		os.Exit(42000) // exit code 42000 is used to indicate that the node should be restarted immediately
		return nil, errors.New(upgradeMsg)
	}
	return &sdk.ResponsePreBlock{
		ConsensusParamsChanged: false,
	}, nil
}

// BuildUpgradeNeededMsg prints the message that notifies that an upgrade is needed.
func BuildUpgradeNeededMsg(plan types.Plan) string {
	return fmt.Sprintf("UPGRADE \"%s\" NEEDED at %s: %s", plan.Name, plan.DueAt(), plan.Info)
}
