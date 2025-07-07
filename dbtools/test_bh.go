package main

import (
	"fmt"
	cometdbm "github.com/cometbft/cometbft-db"
	bs "github.com/cometbft/cometbft/store"
)

func main() {
	sourceDB, _ := cometdbm.NewDB("blockstore", cometdbm.GoLevelDBBackend, "/Users/saadelmadafri/.heliades/data")
	defer sourceDB.Close()
	sourceBlockStore := bs.NewBlockStore(sourceDB)
	
	if blockMeta := sourceBlockStore.LoadBlockMeta(5); blockMeta != nil {
		hashKeyLower := fmt.Sprintf("BH:%x", blockMeta.BlockID.Hash)
		if value, err := sourceDB.Get([]byte(hashKeyLower)); err == nil && value != nil {
			fmt.Printf("✅ BH key (lower) exists: %s => %s\n", hashKeyLower, string(value))
		} else {
			fmt.Printf("❌ BH key (lower) NOT FOUND: %s\n", hashKeyLower)
		}
	}
}
