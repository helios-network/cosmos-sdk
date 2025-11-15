package keeper

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-metrics"

	corestore "cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/log"
	"cosmossdk.io/store/prefix"
	storetypes "cosmossdk.io/store/types"
	xp "cosmossdk.io/x/upgrade/exported"
	"cosmossdk.io/x/upgrade/types"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/kv"
	"github.com/cosmos/cosmos-sdk/types/module"
)

// Deprecated: UpgradeInfoFileName file to store upgrade information
// use x/upgrade/types.UpgradeInfoFilename instead.
const UpgradeInfoFileName string = "upgrade-info.json"

type Keeper struct {
	homePath           string                          // root directory of app config
	skipUpgradeHeights map[int64]bool                  // map of heights to skip for an upgrade
	storeService       corestore.KVStoreService        // key to access x/upgrade store
	cdc                codec.BinaryCodec               // App-wide binary codec
	upgradeHandlers    map[string]types.UpgradeHandler // map of plan name to upgrade handler
	versionSetter      xp.ProtocolVersionSetter        // implements setting the protocol version field on BaseApp
	downgradeVerified  bool                            // tells if we've already sanity checked that this binary version isn't being used against an old state.
	authority          string                          // the address capable of executing and canceling an upgrade. Usually the gov module account
	initVersionMap     module.VersionMap               // the module version map at init genesis
	appVersion         string                          // the current version of the app
	trustedHosts       []string                        // list of trusted hosts to download the upgrade binary from
}

// NewKeeper constructs an upgrade Keeper which requires the following arguments:
// skipUpgradeHeights - map of heights to skip an upgrade
// storeKey - a store key with which to access upgrade's store
// cdc - the app-wide binary codec
// homePath - root directory of the application's config
// vs - the interface implemented by baseapp which allows setting baseapp's protocol version field
func NewKeeper(skipUpgradeHeights map[int64]bool, storeService corestore.KVStoreService, cdc codec.BinaryCodec, homePath string, vs xp.ProtocolVersionSetter, authority string, appVersion string, trustedHosts []string) *Keeper {
	k := &Keeper{
		homePath:           homePath,
		skipUpgradeHeights: skipUpgradeHeights,
		storeService:       storeService,
		cdc:                cdc,
		upgradeHandlers:    map[string]types.UpgradeHandler{},
		versionSetter:      vs,
		authority:          authority,
		appVersion:         appVersion,
		trustedHosts:       trustedHosts,
	}

	// remove empty strings from trustedHosts
	newTrustedHosts := make([]string, 0)
	for _, host := range trustedHosts {
		if host != "" {
			newTrustedHosts = append(newTrustedHosts, host)
		}
	}
	k.trustedHosts = newTrustedHosts

	if upgradePlan, err := k.ReadUpgradeInfoFromDisk(); err == nil && upgradePlan.Height > 0 {
		telemetry.SetGaugeWithLabels([]string{"server", "info"}, 1, []metrics.Label{telemetry.NewLabel("upgrade_height", strconv.FormatInt(upgradePlan.Height, 10))})
	}

	return k
}

// SetVersionSetter sets the interface implemented by baseapp which allows setting baseapp's protocol version field
func (k *Keeper) SetVersionSetter(vs xp.ProtocolVersionSetter) {
	k.versionSetter = vs
}

// GetVersionSetter gets the protocol version field of baseapp
func (k *Keeper) GetVersionSetter() xp.ProtocolVersionSetter {
	return k.versionSetter
}

// SetInitVersionMap sets the initial version map.
// This is only used in app wiring and should not be used in any other context.
func (k *Keeper) SetInitVersionMap(vm module.VersionMap) {
	k.initVersionMap = vm
}

// GetInitVersionMap gets the initial version map
// This is only used in upgrade InitGenesis and should not be used in any other context.
func (k *Keeper) GetInitVersionMap() module.VersionMap {
	return k.initVersionMap
}

// SetTrustedHosts sets the list of trusted hosts to download the upgrade binary from
func (k *Keeper) SetTrustedHosts(trustedHosts []string) {
	k.trustedHosts = trustedHosts
}

// GetTrustedHosts gets the list of trusted hosts to download the upgrade binary from
func (k *Keeper) GetTrustedHosts() []string {
	return k.trustedHosts
}

// SetUpgradeHandler sets an UpgradeHandler for the upgrade specified by name. This handler will be called when the upgrade
// with this name is applied. In order for an upgrade with the given name to proceed, a handler for this upgrade
// must be set even if it is a no-op function.
func (k Keeper) SetUpgradeHandler(name string, upgradeHandler types.UpgradeHandler) {
	k.upgradeHandlers[name] = upgradeHandler
}

// SetModuleVersionMap saves a given version map to state
func (k Keeper) SetModuleVersionMap(ctx context.Context, vm module.VersionMap) error {
	if len(vm) > 0 {
		store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
		versionStore := prefix.NewStore(store, []byte{types.VersionMapByte})
		// Even though the underlying store (cachekv) store is sorted, we still
		// prefer a deterministic iteration order of the map, to avoid undesired
		// surprises if we ever change stores.
		sortedModNames := make([]string, 0, len(vm))

		for key := range vm {
			sortedModNames = append(sortedModNames, key)
		}
		sort.Strings(sortedModNames)

		for _, modName := range sortedModNames {
			ver := vm[modName]
			nameBytes := []byte(modName)
			verBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(verBytes, ver)
			versionStore.Set(nameBytes, verBytes)
		}
	}

	return nil
}

// GetModuleVersionMap returns a map of key module name and value module consensus version
// as defined in ADR-041.
func (k Keeper) GetModuleVersionMap(ctx context.Context) (module.VersionMap, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.VersionMapByte}
	it, err := store.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, err
	}
	defer it.Close()

	vm := make(module.VersionMap)
	for ; it.Valid(); it.Next() {
		moduleBytes := it.Key()
		// first byte is prefix key, so we remove it here
		name := string(moduleBytes[1:])
		moduleVersion := binary.BigEndian.Uint64(it.Value())
		vm[name] = moduleVersion
	}

	return vm, nil
}

// GetModuleVersions gets a slice of module consensus versions
func (k Keeper) GetModuleVersions(ctx context.Context) ([]*types.ModuleVersion, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.VersionMapByte}
	it, err := store.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, err
	}
	defer it.Close()

	mv := make([]*types.ModuleVersion, 0)
	for ; it.Valid(); it.Next() {
		moduleBytes := it.Key()
		name := string(moduleBytes[1:])
		moduleVersion := binary.BigEndian.Uint64(it.Value())
		mv = append(mv, &types.ModuleVersion{
			Name:    name,
			Version: moduleVersion,
		})
	}

	return mv, nil
}

// getModuleVersion gets the version for a given module. If it doesn't exist it returns ErrNoModuleVersionFound, other
// errors may be returned if there is an error reading from the store.
func (k Keeper) getModuleVersion(ctx context.Context, name string) (uint64, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.VersionMapByte}
	it, err := store.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return 0, err
	}
	defer it.Close()

	for ; it.Valid(); it.Next() {
		moduleName := string(it.Key()[1:])
		if moduleName == name {
			version := binary.BigEndian.Uint64(it.Value())
			return version, nil
		}
	}

	return 0, types.ErrNoModuleVersionFound
}

// ScheduleUpgrade schedules an upgrade based on the specified plan.
// If there is another Plan already scheduled, it will cancel and overwrite it.
// ScheduleUpgrade will also write the upgraded IBC ClientState to the upgraded client
// path if it is specified in the plan.
func (k Keeper) ScheduleUpgrade(ctx context.Context, plan types.Plan) error {
	if err := plan.ValidateBasic(); err != nil {
		return err
	}
	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		return err
	}

	if planInfo.Version == "" {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "version cannot be empty")
	}

	if planInfo.Hash == "" {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "hash cannot be empty")
	}

	if planInfo.Size <= 0 {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "size must be greater than 0")
	}

	lastCompletedUpgrade, _, err := k.GetLastCompletedUpgradeVersion(ctx)
	if err == nil && lastCompletedUpgrade != "" && !k.VersionIsOlderThan(lastCompletedUpgrade, planInfo.Version) {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "last completed upgrade version is not older than the plan version")
	}

	// NOTE: allow for the possibility of chains to schedule upgrades in begin block of the same block
	// as a strategy for emergency hard fork recoveries
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if plan.Height < sdkCtx.HeaderInfo().Height {
		return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "upgrade cannot be scheduled in the past")
	}

	doneHeight, err := k.GetDoneHeight(ctx, plan.Name)
	if err != nil {
		return err
	}

	if doneHeight != 0 {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "upgrade with name %s has already been completed", plan.Name)
	}

	store := k.storeService.OpenKVStore(ctx)

	// clear any old IBC state stored by previous plan
	oldPlan, err := k.GetUpgradePlan(ctx)
	// if there's an error but it's not ErrNoUpgradePlanFound, return error
	if err != nil && !errors.Is(err, types.ErrNoUpgradePlanFound) {
		return err
	}

	if err == nil {
		err = k.ClearIBCState(ctx, oldPlan.Height)
		if err != nil {
			return err
		}
	}

	bz, err := k.cdc.Marshal(&plan)
	if err != nil {
		return err
	}

	err = store.Set(types.PlanKey(), bz)
	if err != nil {
		return err
	}

	telemetry.SetGaugeWithLabels([]string{"server", "info"}, 1, []metrics.Label{telemetry.NewLabel("upgrade_height", strconv.FormatInt(plan.Height, 10))})

	return nil
}

// SetUpgradedClient sets the expected upgraded client for the next version of this chain at the last height the current chain will commit.
func (k Keeper) SetUpgradedClient(ctx context.Context, planHeight int64, bz []byte) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Set(types.UpgradedClientKey(planHeight), bz)
}

// GetUpgradedClient gets the expected upgraded client for the next version of this chain. If not found it returns
// ErrNoUpgradedClientFound, but other errors may be returned if there is an error reading from the store.
func (k Keeper) GetUpgradedClient(ctx context.Context, height int64) ([]byte, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.UpgradedClientKey(height))
	if err != nil {
		return nil, err
	}

	if bz == nil {
		return nil, types.ErrNoUpgradedClientFound
	}

	return bz, nil
}

// SetUpgradedConsensusState sets the expected upgraded consensus state for the next version of this chain
// using the last height committed on this chain.
func (k Keeper) SetUpgradedConsensusState(ctx context.Context, planHeight int64, bz []byte) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Set(types.UpgradedConsStateKey(planHeight), bz)
}

// GetUpgradedConsensusState gets the expected upgraded consensus state for the next version of this chain. If not found
// it returns ErrNoUpgradedConsensusStateFound, but other errors may be returned if there is an error reading from the store.
func (k Keeper) GetUpgradedConsensusState(ctx context.Context, lastHeight int64) ([]byte, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.UpgradedConsStateKey(lastHeight))
	if err != nil {
		return nil, err
	}

	if bz == nil {
		return nil, types.ErrNoUpgradedConsensusStateFound
	}

	return bz, nil
}

// GetLastCompletedUpgradeVersion returns the last applied upgrade version.
func (k Keeper) GetLastCompletedUpgradeVersion(ctx context.Context) (string, int64, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.DoneByte}
	it, err := store.ReverseIterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return "", 0, err
	}
	defer it.Close()

	if it.Valid() {
		version, height := parseDoneKey(it.Key())
		return version, height, nil
	}

	return "", 0, nil
}

// parseDoneKey - split upgrade version and height from the done key
func parseDoneKey(key []byte) (string, int64) {
	// 1 byte for the DoneByte + 8 bytes height + at least 1 byte for the name
	kv.AssertKeyAtLeastLength(key, 10)
	height := binary.BigEndian.Uint64(key[1:9])
	return string(key[9:]), int64(height)
}

// encodeDoneKey - concatenate DoneByte, height and upgrade version to form the done key
func encodeDoneKey(version string, height int64) []byte {
	key := make([]byte, 9+len(version)) // 9 = donebyte + uint64 len
	key[0] = types.DoneByte
	binary.BigEndian.PutUint64(key[1:9], uint64(height))
	copy(key[9:], version)
	return key
}

// IsAlreadyApplied returns true if the given upgrade version has already been applied
func (k Keeper) IsAlreadyApplied(ctx context.Context, plan types.Plan) bool {
	store := k.storeService.OpenKVStore(ctx)
	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		return false
	}
	_, err = store.Get(encodeDoneKey(planInfo.Version, plan.Height))
	return err == nil
}

// GetDoneHeight returns the height at which the given upgrade version was executed
func (k Keeper) GetDoneHeight(ctx context.Context, version string) (int64, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.DoneByte}
	it, err := store.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return 0, err
	}
	defer it.Close()

	for ; it.Valid(); it.Next() {
		upgradeVersion, height := parseDoneKey(it.Key())
		if upgradeVersion == version {
			return height, nil
		}
	}

	return 0, nil
}

func (k Keeper) GetAppliedPlans(ctx context.Context) ([]*types.Plan, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.DoneByte}
	it, err := store.ReverseIterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, err
	}
	defer it.Close()
	appliedPlans := make([]*types.Plan, 0)
	for ; it.Valid(); it.Next() {
		var plan types.Plan
		err = k.cdc.Unmarshal(it.Value(), &plan)
		if err != nil {
			return nil, err
		}
		appliedPlans = append(appliedPlans, &plan)
	}
	return appliedPlans, nil
}

func (k Keeper) IsAppliedPlan(ctx context.Context, version string) (bool, error) {
	store := k.storeService.OpenKVStore(ctx)
	prefix := []byte{types.DoneByte}
	it, err := store.ReverseIterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return false, err
	}
	defer it.Close()
	for ; it.Valid(); it.Next() {
		upgradeVersion, _ := parseDoneKey(it.Key())
		if upgradeVersion == version {
			return true, nil
		}
	}
	return false, nil
}

// ClearIBCState clears any planned IBC state
func (k Keeper) ClearIBCState(ctx context.Context, lastHeight int64) error {
	// delete IBC client and consensus state from store if this is IBC plan
	store := k.storeService.OpenKVStore(ctx)
	err := store.Delete(types.UpgradedClientKey(lastHeight))
	if err != nil {
		return err
	}

	return store.Delete(types.UpgradedConsStateKey(lastHeight))
}

// ClearUpgradePlan clears any schedule upgrade and associated IBC states.
func (k Keeper) ClearUpgradePlan(ctx context.Context) error {
	// clear IBC states every time upgrade plan is removed
	oldPlan, err := k.GetUpgradePlan(ctx)
	if err != nil {
		// if there's no upgrade plan, return nil to match previous behavior
		if errors.Is(err, types.ErrNoUpgradePlanFound) {
			return nil
		}
		return err
	}

	err = k.ClearIBCState(ctx, oldPlan.Height)
	if err != nil {
		return err
	}

	store := k.storeService.OpenKVStore(ctx)
	return store.Delete(types.PlanKey())
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

// GetUpgradePlan returns the currently scheduled Plan if any. If not found it returns
// ErrNoUpgradePlanFound, but other errors may be returned if there is an error reading from the store.
func (k Keeper) GetUpgradePlan(ctx context.Context) (plan types.Plan, err error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.PlanKey())
	if err != nil {
		return plan, err
	}

	if bz == nil {
		return plan, types.ErrNoUpgradePlanFound
	}

	err = k.cdc.Unmarshal(bz, &plan)
	if err != nil {
		return plan, err
	}

	return plan, err
}

// setDone marks this upgrade version as being done so the version can't be reused accidentally
func (k Keeper) setDone(ctx context.Context, plan types.Plan) error {
	store := k.storeService.OpenKVStore(ctx)
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	bz, err := k.cdc.Marshal(&plan)
	if err != nil {
		return err
	}
	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		return err
	}
	return store.Set(encodeDoneKey(planInfo.Version, sdkCtx.HeaderInfo().Height), bz)
}

// HasHandler returns true iff there is a handler registered for this name
func (k Keeper) HasHandler(name string) bool {
	_, ok := k.upgradeHandlers[name]
	return ok
}

// ApplyUpgrade will execute the upgrade binary and mark the plan as done.
func (k Keeper) ApplyUpgrade(ctx context.Context, plan types.Plan) error {
	upgradeBinaryDirPath := k.GetUpgradeBinaryDirPath()
	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		return fmt.Errorf("failed to get plan info: %w", err)
	}
	version := planInfo.Version
	upgradeBinaryPath := filepath.Join(upgradeBinaryDirPath, version)
	// locate heliades binary path
	heliadesPath := k.GetHeliadesBinaryPath()
	// move downloaded binary to the actual heliades binary path
	err = os.Rename(upgradeBinaryPath, heliadesPath)
	if err != nil {
		return fmt.Errorf("failed to move upgrade binary to heliades binary path: %w", err)
	}
	fmt.Println("Upgrade binary moved to", heliadesPath)
	return k.setDone(ctx, plan)
}

// IsSkipHeight checks if the given height is part of skipUpgradeHeights
func (k Keeper) IsSkipHeight(height int64) bool {
	return k.skipUpgradeHeights[height]
}

// DumpUpgradeInfoToDisk writes upgrade information to UpgradeInfoFileName.
func (k Keeper) DumpUpgradeInfoToDisk(height int64, p types.Plan) error {
	upgradeInfoFilePath, err := k.GetUpgradeInfoPath()
	if err != nil {
		return err
	}

	upgradeInfo := types.Plan{
		Name:   p.Name,
		Height: height,
		Info:   p.Info,
	}
	info, err := json.Marshal(upgradeInfo)
	if err != nil {
		return err
	}

	return os.WriteFile(upgradeInfoFilePath, info, 0o755)
}

// GetUpgradeInfoPath returns the upgrade info file path
func (k Keeper) GetUpgradeInfoPath() (string, error) {
	upgradeInfoFileDir := path.Join(k.getHomeDir(), "data")
	if err := os.MkdirAll(upgradeInfoFileDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("could not create directory %q: %w", upgradeInfoFileDir, err)
	}

	return filepath.Join(upgradeInfoFileDir, types.UpgradeInfoFilename), nil
}

func (k Keeper) GetUpgradeBinaryDirPath() string {
	return path.Join(k.getHomeDir(), "upgrades-binaries")
}

// getHomeDir returns the height at which the given upgrade was executed
func (k Keeper) getHomeDir() string {
	return k.homePath
}

// ReadUpgradeInfoFromDisk returns the name and height of the upgrade which is
// written to disk by the old binary when panicking. An error is returned if
// the upgrade path directory cannot be created or if the file exists and
// cannot be read or if the upgrade info fails to unmarshal.
func (k Keeper) ReadUpgradeInfoFromDisk() (types.Plan, error) {
	var upgradeInfo types.Plan

	upgradeInfoPath, err := k.GetUpgradeInfoPath()
	if err != nil {
		return upgradeInfo, err
	}

	data, err := os.ReadFile(upgradeInfoPath)
	if err != nil {
		// if file does not exist, assume there are no upgrades
		if os.IsNotExist(err) {
			return upgradeInfo, nil
		}

		return upgradeInfo, err
	}

	if err := json.Unmarshal(data, &upgradeInfo); err != nil {
		return upgradeInfo, err
	}

	if err := upgradeInfo.ValidateBasic(); err != nil {
		return upgradeInfo, err
	}

	return upgradeInfo, nil
}

// SetDowngradeVerified updates downgradeVerified.
func (k *Keeper) SetDowngradeVerified(v bool) {
	k.downgradeVerified = v
}

// DowngradeVerified returns downgradeVerified.
func (k Keeper) DowngradeVerified() bool {
	return k.downgradeVerified
}

func (k Keeper) VerifyIfTheBinaryHasBeenDownloadedForThePlan(plan types.Plan) bool {
	upgradeBinaryDirPath := k.GetUpgradeBinaryDirPath()
	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		fmt.Println("Error getting plan info:", err)
		return false
	}
	fmt.Println("Plan info", planInfo)

	version := planInfo.Version
	upgradeBinaryPath := filepath.Join(upgradeBinaryDirPath, version)

	stat, err := os.Stat(upgradeBinaryPath)
	if err != nil {
		fmt.Println("File does not exist or stat error:", err)
		return false
	}

	// Check size
	if stat.Size() != planInfo.Size {
		fmt.Printf("Size mismatch: file=%d plan=%d\n", stat.Size(), planInfo.Size)
		return false
	}

	// Open the file and hash in streaming (avoid allocating all in memory)
	f, err := os.Open(upgradeBinaryPath)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return false
	}
	defer f.Close()

	hasher := sha256.New()
	written, err := io.Copy(hasher, f)
	if err != nil {
		fmt.Println("Error hashing file:", err)
		return false
	}
	_ = written // to debug if needed

	hashBytes := hasher.Sum(nil) // hash correct of 32 bytes
	actualHash := hex.EncodeToString(hashBytes)

	// Normalize plan hash (remove 0x if present, and in lowercase)
	expectedHash := planInfo.Hash
	if strings.HasPrefix(expectedHash, "0x") || strings.HasPrefix(expectedHash, "0X") {
		expectedHash = expectedHash[2:]
	}
	expectedHash = strings.ToLower(expectedHash)

	fmt.Println("Hash of the binary", actualHash)
	fmt.Println("Hash of the plan info", expectedHash)

	// Constant time comparison
	if len(expectedHash) != len(actualHash) {
		return false
	}
	// subtle.ConstantTimeCompare operates on []byte
	match := subtle.ConstantTimeCompare([]byte(actualHash), []byte(expectedHash)) == 1
	return match
}

func (k Keeper) TryDownloadUpgradeBinary(
	ctx context.Context,
	plan types.Plan,
	trustedHosts []string,
	extension string,
) error {
	upgradeBinaryDirPath := k.GetUpgradeBinaryDirPath()

	if _, err := os.Stat(upgradeBinaryDirPath); os.IsNotExist(err) {
		if err := os.MkdirAll(upgradeBinaryDirPath, 0o755); err != nil {
			return fmt.Errorf("failed to create upgrade binary directory: %w", err)
		}
	}

	planInfo, err := plan.GetPlanInfo()
	if err != nil {
		return err
	}

	version := planInfo.Version
	upgradeBinaryPath := filepath.Join(upgradeBinaryDirPath, version)
	fmt.Println("Upgrade binary path:", upgradeBinaryPath)

	for _, trustedHost := range trustedHosts {
		host := strings.TrimSuffix(trustedHost, "/")
		url := fmt.Sprintf("%s/%s/%s", host, version, extension)
		fmt.Println("Trying URL:", url)

		resp, err := http.Get(url)
		if err != nil {
			fmt.Println("Error on GET:", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			fmt.Println("Non 200 status code:", resp.StatusCode)
			resp.Body.Close()
			continue
		}

		var reader io.Reader = resp.Body

		// Decompression streaming si fichier gz
		if strings.HasSuffix(url, ".gz") {
			fmt.Println("Streaming decompression (gzip)")
			gz, err := gzip.NewReader(resp.Body)
			if err != nil {
				resp.Body.Close()
				fmt.Println("Error creating gzip reader:", err)
				continue
			}
			defer gz.Close()
			reader = gz
		}

		// Création d'un fichier temporaire → sécurité si erreur
		tmpPath := upgradeBinaryPath + ".tmp"
		out, err := os.Create(tmpPath)
		if err != nil {
			resp.Body.Close()
			return fmt.Errorf("failed to create file: %w", err)
		}

		hasher := sha256.New()

		// Copier le stream dans le fichier et dans le hasher simultanément
		_, err = io.Copy(io.MultiWriter(out, hasher), reader)

		resp.Body.Close()
		out.Close()

		if err != nil {
			os.Remove(tmpPath)
			fmt.Println("Error during streaming download:", err)
			continue
		}

		// Vérification du hash
		actualHash := hex.EncodeToString(hasher.Sum(nil))

		if strings.HasPrefix(planInfo.Hash, "0x") {
			planInfo.Hash = strings.Replace(planInfo.Hash, "0x", "", 1)
		}
		if actualHash != planInfo.Hash {
			fmt.Println("Hash mismatch, deleting temp file", actualHash, planInfo.Hash)
			os.Remove(tmpPath)
			continue
		}

		// Hash OK → rename
		if err := os.Rename(tmpPath, upgradeBinaryPath); err != nil {
			fmt.Println("Error renaming file:", err)
			continue
		}

		if err := os.Chmod(upgradeBinaryPath, 0o755); err != nil {
			fmt.Println("Warning: failed to chmod binary:", err)
		}

		fmt.Println("Download + verification OK ✅")
		return nil
	}

	return fmt.Errorf("failed to download binary from trusted hosts")
}

func (k Keeper) GetHeliadesBinaryPath() string {
	command := exec.Command("which", "heliades")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (k Keeper) GetAppVersion(ctx context.Context) string {
	return k.appVersion
}

// ParseVersion enlève le préfixe 'v' si présent
func (k Keeper) ParseVersion(version string) string {
	return strings.TrimPrefix(version, "v")
}

// VersionIsOlderThan retourne true si appVersion < planVersion (SemVer-compatible)
func (k Keeper) VersionIsOlderThan(appVersion string, planVersion string) bool {
	app := k.ParseVersion(appVersion)
	plan := k.ParseVersion(planVersion)

	// Split pre-release (ex: 1.0.0-alpha)
	appMain, appPre := splitVersion(app)
	planMain, planPre := splitVersion(plan)

	// Compare major.minor.patch
	if cmp := compareMainParts(appMain, planMain); cmp != 0 {
		return cmp < 0
	}

	// Si version principale identique → gérer pré-release (alpha, beta, etc.)
	return comparePreRelease(appPre, planPre) < 0
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

// Sépare "1.0.1-alpha" → "1.0.1" , "alpha"
func splitVersion(version string) (main string, pre string) {
	if strings.Contains(version, "-") {
		parts := strings.SplitN(version, "-", 2)
		return parts[0], parts[1]
	}
	return version, ""
}

// Compare "1.0.10" et "1.0.2"
func compareMainParts(v1, v2 string) int {
	p1 := strings.Split(v1, ".")
	p2 := strings.Split(v2, ".")

	for i := 0; i < len(p1) || i < len(p2); i++ {
		var n1, n2 int

		if i < len(p1) {
			n1, _ = strconv.Atoi(p1[i])
		}
		if i < len(p2) {
			n2, _ = strconv.Atoi(p2[i])
		}

		if n1 != n2 {
			if n1 < n2 {
				return -1
			}
			return 1
		}
	}

	return 0
}

// Compare pré-release selon SemVer : "" > alpha > beta > rc
func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}

	// Version release (sans suffixe) > pré-release
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}

	priority := map[string]int{
		"alpha": 1,
		"beta":  2,
		"rc":    3,
	}

	// Alpha < Beta < RC (par défaut comparaison alpha-numérique)
	pa := priority[a]
	pb := priority[b]

	return pa - pb
}
