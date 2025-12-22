package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// KeySyncCmd is the cobra.Command to rencrypt and sync all encrypt keys for a
// user.
var KeySyncCmd = &cobra.Command{
	Use:    "sync-keys",
	Hidden: true,
	Short:  "Re-encrypt encrypt keys for all linked public keys",
	Long:   paragraph(fmt.Sprintf("%s encrypt keys for all linked public keys", keyword("Re-encrypt"))),
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cc, err := initCharmClient()
		if err != nil {
			return err
		}
		if err := cc.SyncEncryptKeys(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Synced encryption keys")
		return nil
	},
}
