package keys

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
)

// DeleteKeyCommand deletes a key from the key store.
func ClearKeyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete All keys",
		Long: `Delete keys from the Keybase backend.

Note that removing offline or ledger keys will remove
only the public key references stored locally, i.e.
private keys stored in a ledger device cannot be deleted with the CLI.
`,
		Args: cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			records, err := clientCtx.Keyring.List()
			if err != nil {
				return err
			}

			for _, record := range records {

				if err := clientCtx.Keyring.Delete(record.Name); err != nil {
					return err
				}
				cmd.PrintErrln(fmt.Sprintf("Key %s deleted forever (uh oh!)", record.Name))
			}

			return nil
		},
	}

	return cmd
}
