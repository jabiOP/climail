package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSyncCmd(app *App) *cobra.Command {
	var full bool
	var account string
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync with Gmail now (every account, or the one given)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			accounts, err := app.Accounts(ctx)
			if err != nil {
				return err
			}
			if account != "" {
				one, err := app.Account(ctx, account)
				if err != nil {
					return err
				}
				accounts = []string{one}
			}
			if full {
				for _, a := range accounts {
					if err := app.store.SetHistoryID(ctx, a, 0); err != nil {
						return err
					}
				}
			}
			var progress func(string, int, int)
			if full {
				progress = app.progressPrinter()
			}
			ran, err := app.SyncNow(ctx, progress, accounts...)
			if progress != nil {
				fmt.Fprintln(app.Stderr)
			}
			if err != nil {
				return err
			}
			if !ran {
				fmt.Fprintln(app.Stderr, "climail: another sync is running; it will pick up your changes")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "download recent mail again (fixes a confused cache)")
	addAccountFlag(cmd, &account)
	return cmd
}
