package main

import (
	"fmt"
	"os"
	
	cometdbm "github.com/cometbft/cometbft-db"
	sm "github.com/cometbft/cometbft/state"
)

func main() {
	homeDir := os.Getenv("HOME") + "/.heliades"
	
	stateDB, err := cometdbm.NewGoLevelDB("state", homeDir+"/data")
	if err != nil {
		fmt.Printf("Error opening state db: %v\n", err)
		os.Exit(1)
	}
	defer stateDB.Close()

	stateStore := sm.NewStore(stateDB, sm.StoreOptions{})
	state, err := stateStore.Load()
	if err != nil {
		fmt.Printf("Error loading state: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Current blockchain state:\n")
	fmt.Printf("  Height: %d\n", state.LastBlockHeight)
	fmt.Printf("  AppHash: %X\n", state.AppHash)
	fmt.Printf("  ChainID: %s\n", state.ChainID)
}
