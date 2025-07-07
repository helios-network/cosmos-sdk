package main

import (
	"fmt"
	"os"

	cometdbm "github.com/cometbft/cometbft-db"
	bs "github.com/cometbft/cometbft/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run test_blockstore.go <path_to_data>")
		os.Exit(1)
	}

	dataPath := os.Args[1]
	fmt.Printf("Testing blockstore at: %s\n", dataPath)

	// Open blockstore database
	db, err := cometdbm.NewGoLevelDB("blockstore", dataPath)
	if err != nil {
		fmt.Printf("Error opening blockstore: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create blockstore
	blockStore := bs.NewBlockStore(db)

	fmt.Printf("Blockstore height: %d\n", blockStore.Height())
	fmt.Printf("Blockstore base: %d\n", blockStore.Base())

	// Test loading blocks
	for i := int64(1); i <= 5; i++ {
		block := blockStore.LoadBlock(i)
		if block != nil {
			fmt.Printf("Block %d: EXISTS (hash: %X)\n", i, block.Hash())
		} else {
			fmt.Printf("Block %d: NOT FOUND\n", i)
		}
	}
}
