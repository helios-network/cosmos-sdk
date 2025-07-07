package main

import (
	"fmt"
	cometdbm "github.com/cometbft/cometbft-db"
	bs "github.com/cometbft/cometbft/store"
)

func main() {
	db, _ := cometdbm.NewDB("blockstore", cometdbm.GoLevelDBBackend, "exported_data_5")
	defer db.Close()
	
	fmt.Println("=== Keys in exported blockstore ===")
	iter, _ := db.Iterator(nil, nil)
	defer iter.Close()
	
	count := 0
	for ; iter.Valid() && count < 20; iter.Next() {
		key := string(iter.Key())
		fmt.Printf("Key: %s\n", key)
		count++
	}
	
	fmt.Println("\n=== Testing blockstore ===")
	store := bs.NewBlockStore(db)
	fmt.Printf("Height: %d, Base: %d\n", store.Height(), store.Base())
	
	// Test direct block access
	if block := store.LoadBlock(5); block != nil {
		fmt.Println("✅ Block 5 found via LoadBlock")
	} else {
		fmt.Println("❌ Block 5 NOT found via LoadBlock")
	}
}
