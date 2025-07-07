package main

import (
	"fmt"
	cometdbm "github.com/cometbft/cometbft-db"
)

func main() {
	// Original
	orig, _ := cometdbm.NewDB("blockstore", cometdbm.GoLevelDBBackend, "/Users/saadelmadafri/.heliades/data")
	defer orig.Close()
	
	// Exported
	exp, _ := cometdbm.NewDB("blockstore", cometdbm.GoLevelDBBackend, "exported_data_5")
	defer exp.Close()
	
	// Check specific keys for block 5
	keys := []string{"H:5", "B:5", "C:5", "SC:5", "P:5"}
	
	for _, key := range keys {
		origVal, origErr := orig.Get([]byte(key))
		expVal, expErr := exp.Get([]byte(key))
		
		origExists := origErr == nil && origVal != nil
		expExists := expErr == nil && expVal != nil
		
		fmt.Printf("Key %s: Original=%v, Exported=%v\n", key, origExists, expExists)
	}
}
