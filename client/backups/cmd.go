package backups

import (
	"github.com/spf13/cobra"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// Cmd returns the backup commands
func Cmd(appCreator servertypes.AppCreator, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backups",
		Short: "Backup and restore commands",
		Long:  "Commands for creating and restoring backups of the application state",
	}

	cmd.AddCommand(
		DumpCmd(appCreator, defaultNodeHome),
		RestoreCmd(appCreator, defaultNodeHome),
	)

	return cmd
}
