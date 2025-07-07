package main

import (
	"fmt"
	"os"

	"github.com/cosmos/cosmos-sdk/client/db"
)

func main() {
	cmd := db.ExportDataCmd(os.Getenv("HOME") + "/.heliades")
	cmd.SetArgs(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
