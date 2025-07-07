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
	
	// Check blockStore metadata
	origMeta, _ := orig.Get([]byte("blockStore"))
	expMeta, _ := exp.Get([]byte("blockStore"))
	
	fmt.Printf("Original blockStore: %x (len=%d)\n", origMeta, len(origMeta))
	fmt.Printf("Exported blockStore: %x (len=%d)\n", expMeta, len(expMeta))
	
	// Simple comparison
	if len(origMeta) == len(expMeta) {
		fmt.Println("Same length")
	} else {
		fmt.Println("Different length!")
	}
}
