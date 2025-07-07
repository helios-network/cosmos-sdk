package main

import (
	"fmt"
	"bytes"
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
	keys := []string{"H:5", "C:5", "SC:5"}
	
	for _, key := range keys {
		origVal, _ := orig.Get([]byte(key))
		expVal, _ := exp.Get([]byte(key))
		
		if bytes.Equal(origVal, expVal) {
			fmt.Printf("Key %s: ✅ IDENTICAL (length: %d)\n", key, len(origVal))
		} else {
			fmt.Printf("Key %s: ❌ DIFFERENT (orig: %d, exp: %d)\n", key, len(origVal), len(expVal))
		}
	}
	
	// Check BH key
	bhKey := "BH:3baaaf6e928def38cfdf26ad02c34176205caeab7354ce9ba510db48c6e67729"
	origBH, _ := orig.Get([]byte(bhKey))
	expBH, _ := exp.Get([]byte(bhKey))
	
	if bytes.Equal(origBH, expBH) {
		fmt.Printf("Key %s: ✅ IDENTICAL (value: %s)\n", bhKey, string(origBH))
	} else {
		fmt.Printf("Key %s: ❌ DIFFERENT (orig: %s, exp: %s)\n", bhKey, string(origBH), string(expBH))
	}
}
