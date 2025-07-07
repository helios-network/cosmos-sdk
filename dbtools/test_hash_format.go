package main

import (
	"fmt"
	"encoding/hex"
	cometdbm "github.com/cometbft/cometbft-db"
	bs "github.com/cometbft/cometbft/store"
)

func main() {
	sourceDB, _ := cometdbm.NewDB("blockstore", cometdbm.GoLevelDBBackend, "/Users/saadelmadafri/.heliades/data")
	defer sourceDB.Close()
	sourceBlockStore := bs.NewBlockStore(sourceDB)
	
	if blockMeta := sourceBlockStore.LoadBlockMeta(5); blockMeta != nil {
		hash := blockMeta.BlockID.Hash
		fmt.Printf("Hash bytes: %v\n", hash)
		fmt.Printf("Hash %%x: %x\n", hash)
		fmt.Printf("Hash %%X: %X\n", hash)
		fmt.Printf("Hash hex.EncodeToString: %s\n", hex.EncodeToString(hash))
		
		// Test different formats
		formats := []string{
			fmt.Sprintf("BH:%x", hash),
			fmt.Sprintf("BH:%X", hash),
			fmt.Sprintf("BH:%s", hex.EncodeToString(hash)),
		}
		
		for _, key := range formats {
			if value, err := sourceDB.Get([]byte(key)); err == nil && value != nil {
				fmt.Printf("✅ Found: %s => %s\n", key, string(value))
			} else {
				fmt.Printf("❌ Not found: %s\n", key)
			}
		}
	}
}
