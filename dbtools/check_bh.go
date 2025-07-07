package main

import (
	"fmt"
	cometdbm "github.com/cometbft/cometbft-db"
)

func main() {
	sourceDB, _ := cometdbm.NewDB("blockstore", cometdbm.GoLevelDBBackend, "/Users/saadelmadafri/.heliades/data")
	defer sourceDB.Close()
	
	// Check exact BH key from debug output
	exactKey := "BH:3baaaf6e928def38cfdf26ad02c34176205caeab7354ce9ba510db48c6e67729"
	if value, err := sourceDB.Get([]byte(exactKey)); err == nil && value != nil {
		fmt.Printf("✅ Exact BH key exists: %s => %s\n", exactKey, string(value))
	} else {
		fmt.Printf("❌ Exact BH key NOT FOUND: %s\n", exactKey)
	}
}
