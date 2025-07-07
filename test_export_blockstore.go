package main

import (
	"flag"
	"fmt"
	"log"

	cometdbm "github.com/cometbft/cometbft-db"
	bs "github.com/cometbft/cometbft/store"
)

func main() {
	dir := flag.String("dir", "", "Directory containing the blockstore")
	flag.Parse()

	if *dir == "" {
		log.Fatal("Please provide -dir flag")
	}

	// Open the blockstore database
	db, err := cometdbm.NewGoLevelDB("blockstore", *dir)
	if err != nil {
		log.Fatalf("Failed to open blockstore: %v", err)
	}
	defer db.Close()

	blockStore := bs.NewBlockStore(db)

	fmt.Printf("Blockstore info:\n")
	fmt.Printf("  Height: %d\n", blockStore.Height())
	fmt.Printf("  Base: %d\n", blockStore.Base())

	// Test if we can load specific blocks
	for height := int64(1); height <= blockStore.Height(); height++ {
		block := blockStore.LoadBlock(height)
		if block != nil {
			fmt.Printf("  Block %d: %X\n", height, block.Hash())
		} else {
			fmt.Printf("  Block %d: NOT FOUND\n", height)
		}
	}
}
