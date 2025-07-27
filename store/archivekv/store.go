package archivekv

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"cosmossdk.io/store/cachekv"
	"cosmossdk.io/store/dbadapter"
	"cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"
	gogotypes "github.com/cosmos/gogoproto/types"
)

const (
	latestVersionKey = "s/latest"
	commitInfoKeyFmt = "s/%d" // s/<version>
	baseVersionKey   = "s/base"
)

// CommitData represents the data committed at a specific height
type CommitData struct {
	Height         uint64   `json:"height"`
	NewKeysCreated []string `json:"new_keys_created"`
}

// Store is a simple wrapper around cachekv.Store for archiving data
type Store struct {
	mtx          sync.Mutex
	name         string
	mem          dbadapter.Store
	cacheKVStore *cachekv.Store
}

// NewStore creates a new ArchiveKVStore
func NewStore(name string, archiveDB dbm.DB) *Store {
	mem := dbadapter.Store{DB: archiveDB}
	cacheKVStore := cachekv.NewStore(mem)

	return &Store{
		name:         name,
		mem:          mem,
		cacheKVStore: cacheKVStore,
	}
}

// getBaseHeight retrieves the base height from the database
func (store *Store) getBaseHeight() uint64 {
	bz := store.mem.Get([]byte(baseVersionKey))
	if bz == nil {
		return 0
	}

	var baseHeight int64
	if err := gogotypes.StdInt64Unmarshal(&baseHeight, bz); err != nil {
		return 0
	}
	return uint64(baseHeight)
}

// flushBaseHeight updates the base height in the database
func (store *Store) flushBaseHeight(height uint64) {
	bz, err := gogotypes.StdInt64Marshal(int64(height))
	if err != nil {
		panic(fmt.Sprintf("failed to marshal base height: %v", err))
	}
	store.mem.Set([]byte(baseVersionKey), bz)
}

// getLatestHeight retrieves the latest height from the database
func (store *Store) getLatestHeight() uint64 {
	bz := store.mem.Get([]byte(latestVersionKey))
	if bz == nil {
		return 0
	}

	var latestHeight int64
	if err := gogotypes.StdInt64Unmarshal(&latestHeight, bz); err != nil {
		return 0
	}
	return uint64(latestHeight)
}

// flushLatestHeight updates the latest height in the database
func (store *Store) flushLatestHeight(height uint64) {
	bz, err := gogotypes.StdInt64Marshal(int64(height))
	if err != nil {
		panic(fmt.Sprintf("failed to marshal latest height: %v", err))
	}
	store.mem.Set([]byte(latestVersionKey), bz)
}

// ArchiveKVStore interface methods
func (store *Store) ArchiveName() string {
	return store.name
}

func (store *Store) ArchiveVersion() int64 {
	return int64(store.getBaseHeight())
}

func (store *Store) SetArchiveVersion(version int64) {
	store.flushBaseHeight(uint64(version))
}

// Commit archives the current state at the given height
func (store *Store) Commit(height uint64) error {
	store.mtx.Lock()
	defer store.mtx.Unlock()

	// Get the trace to see what was modified
	trace := store.cacheKVStore.DumpTrace()

	// Create commit data
	commitData := CommitData{
		Height:         height,
		NewKeysCreated: make([]string, 0),
	}

	// Extract new keys from trace
	for key, cValue := range trace.CacheSorted {
		if cValue.Value != nil {
			// Check if this is a new key (wasn't in the store before)
			if !store.mem.Has([]byte(key)) {
				commitData.NewKeysCreated = append(commitData.NewKeysCreated, key)
			}
		}
	}

	// Only save if there are new keys created
	if len(commitData.NewKeysCreated) > 0 {
		// Serialize commit data
		data, err := json.Marshal(commitData)
		if err != nil {
			return fmt.Errorf("failed to marshal commit data: %w", err)
		}

		// Save to database with key "s/<height>"
		key := fmt.Sprintf(commitInfoKeyFmt, height)
		store.mem.Set([]byte(key), data)

		// Update latest height
		store.flushLatestHeight(height)

		fmt.Printf("Store %s committed %d new keys at height %d\n",
			store.name, len(commitData.NewKeysCreated), height)
	}

	// Write changes to underlying store
	store.cacheKVStore.Write()

	return nil
}

// ArchiveData is an alias for Commit for compatibility
func (store *Store) ArchiveData(version int64) error {
	return store.Commit(uint64(version))
}

// DeleteArchivedData removes archived data for a specific version
func (store *Store) DeleteArchivedData(version int64) error {
	store.mtx.Lock()
	defer store.mtx.Unlock()

	key := fmt.Sprintf(commitInfoKeyFmt, version)
	store.mem.Delete([]byte(key))
	return nil
}

// RollbackToVersion rolls back to a specific version
func (store *Store) RollbackToVersion(version int64) error {
	store.mtx.Lock()
	defer store.mtx.Unlock()

	// Check if target version exists
	targetVersion := int64(version)
	key := fmt.Sprintf(commitInfoKeyFmt, targetVersion)
	data := store.mem.Get([]byte(key))
	if data == nil {
		return fmt.Errorf("target version %d not found", targetVersion)
	}

	// Get latest height from database
	latestHeight := store.getLatestHeight()
	if latestHeight == 0 {
		return fmt.Errorf("no versions available for rollback")
	}

	// If we're already at the target version, no need to rollback
	if targetVersion == int64(latestHeight) {
		fmt.Printf("Store %s already at version %d, no rollback needed\n", store.name, targetVersion)
		return nil
	}

	fmt.Printf("Store %s rolling back from version %d to version %d\n", store.name, latestHeight, targetVersion)

	// Start from the latest version and go backwards to targetVersion
	// We need to undo all commits from latestHeight down to targetVersion + 1
	for v := int64(latestHeight); v > targetVersion; v-- {
		// Load commit data for this version
		key := fmt.Sprintf(commitInfoKeyFmt, v)
		data := store.mem.Get([]byte(key))
		if data == nil {
			fmt.Printf("Warning: commit data for version %d not found, skipping\n", v)
			continue
		}

		// Deserialize commit data
		var commitData CommitData
		err := json.Unmarshal(data, &commitData)
		if err != nil {
			fmt.Printf("Warning: failed to unmarshal commit data for version %d: %v, skipping\n", v, err)
			continue
		}

		// Delete each new key that was created in this version
		for _, key := range commitData.NewKeysCreated {
			store.cacheKVStore.Delete([]byte(key))
		}

		// Delete the commit record for this version
		store.mem.Delete([]byte(key))
	}

	// Write the rolled back state to the underlying store
	store.cacheKVStore.Write()

	// Update latest height to the target version
	store.flushLatestHeight(uint64(targetVersion))

	fmt.Printf("Store %s successfully rolled back to version %d\n", store.name, targetVersion)

	return nil
}

// PruneVersions prunes versions up to the specified version
func (store *Store) PruneVersions(version int64) error {
	return store.DeleteFromBaseVersionTo(uint64(version))
}

// DeleteFromBaseVersionTo deletes versions from base version to target version
func (store *Store) DeleteFromBaseVersionTo(retainHeight uint64) error {
	store.mtx.Lock()
	defer store.mtx.Unlock()

	// Get current base height
	baseHeight := store.getBaseHeight()

	// If baseHeight is 0, set it to the first committed height
	if baseHeight == 0 {
		iter := store.mem.Iterator(nil, nil)
		defer iter.Close()

		for ; iter.Valid(); iter.Next() {
			key := string(iter.Key())
			if len(key) > 2 && key[:2] == "s/" {
				var height uint64
				_, err := fmt.Sscanf(key, "s/%d", &height)
				if err == nil {
					baseHeight = height
					break
				}
			}
		}
	}

	// Read all commits from baseHeight to retainHeight and collect keys to delete
	keysToDelete := make(map[string]bool)

	for height := baseHeight; height <= retainHeight; height++ {
		commitKey := fmt.Sprintf(commitInfoKeyFmt, height)
		data := store.mem.Get([]byte(commitKey))
		if data == nil {
			continue // Skip if commit doesn't exist
		}

		// Deserialize commit data
		var commitData CommitData
		err := json.Unmarshal(data, &commitData)
		if err != nil {
			fmt.Printf("Warning: failed to unmarshal commit data for height %d: %v\n", height, err)
			continue
		}

		// Add new keys created in this commit to deletion list
		for _, key := range commitData.NewKeysCreated {
			keysToDelete[key] = true
		}

		// Delete the commit record
		store.mem.Delete([]byte(commitKey))
	}

	// Delete all the collected keys from the store
	for key := range keysToDelete {
		store.mem.Delete([]byte(key))
	}

	// Update base height
	store.flushBaseHeight(retainHeight + 1)

	fmt.Printf("Store %s deleted %d keys and commits from height %d to %d, new baseHeight: %d\n",
		store.name, len(keysToDelete), baseHeight, retainHeight, retainHeight+1)

	return nil
}

// GetBaseVersion returns the current base version
func (store *Store) GetBaseVersion() int64 {
	return int64(store.getBaseHeight())
}

// LatestHeight returns the latest height
func (store *Store) LatestHeight() int64 {
	return int64(store.getLatestHeight())
}

// CacheKVStore interface methods - proxy to cacheKVStore
func (store *Store) GetStoreType() types.StoreType {
	return store.cacheKVStore.GetStoreType()
}

func (store *Store) Get(key []byte) []byte {
	return store.cacheKVStore.Get(key)
}

func (store *Store) Set(key, value []byte) {
	store.cacheKVStore.Set(key, value)
}

func (store *Store) Delete(key []byte) {
	store.cacheKVStore.Delete(key)
}

func (store *Store) Has(key []byte) bool {
	return store.cacheKVStore.Has(key)
}

func (store *Store) Write() {
	store.cacheKVStore.Write()
}

func (store *Store) DumpTrace() types.TraceCommit {
	return store.cacheKVStore.DumpTrace()
}

func (store *Store) Copy() types.CacheKVStore {
	return store.cacheKVStore.Copy()
}

func (store *Store) CacheWrap() types.CacheWrap {
	return store.cacheKVStore.CacheWrap()
}

func (store *Store) CacheWrapWithTrace(w io.Writer, tc types.TraceContext) types.CacheWrap {
	return store.cacheKVStore.CacheWrapWithTrace(w, tc)
}

func (store *Store) Iterator(start, end []byte) types.Iterator {
	return store.cacheKVStore.Iterator(start, end)
}

func (store *Store) ReverseIterator(start, end []byte) types.Iterator {
	return store.cacheKVStore.ReverseIterator(start, end)
}
