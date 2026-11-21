package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/auth"
	"github.com/jabiOP/climail/internal/gmail"
)

func newAuthCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage your Google accounts"}
	cmd.AddCommand(newAuthSetupCmd(app), newAuthLoginCmd(app), newAuthStatusCmd(app), newAuthLogoutCmd(app))
	return cmd
}

func newAuthSetupCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "setup CLIENT_SECRET.json",
		Short: "Install your Google OAuth client (see docs/setup-google-cloud.md)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := auth.LoadClient(args[0]); err != nil { // validate before copying
				return err
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			if err := app.Paths.EnsureDirs(); err != nil {
				return err
			}
			if err := os.WriteFile(app.Paths.ClientFile(), data, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(app.Stderr, "Saved to %s. Next: climail auth login\n", app.Paths.ClientFile())
			return nil
		},
	}
}

func newAuthLoginCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Add a Google account (log in in your browser), then download its recent mail",
		Long:  "Add a Google account: log in in your browser, then download its recent mail. Run it again to add another account.",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := auth.LoadClient(app.Paths.ClientFile())
			if err != nil {
				return err
			}
			st, err := app.Store()
			if err != nil {
				return err
			}
			tok, err := auth.Login(ctx, cfg, func(url string) error {
				fmt.Fprintf(app.Stderr, "Opening your browser. If it doesn't open, visit:\n\n  %s\n\n", url)
				if app.OpenURL != nil {
					_ = app.OpenURL(url) // the printed URL is the fallback
				}
				return nil
			})
			if err != nil {
				return err
			}
			client, err := gmail.NewClient(ctx, "", cfg.Client(ctx, tok))
			if err != nil {
				return err
			}
			email, _, err := client.Profile(ctx)
			if err != nil {
				return err
			}
			if err := auth.DefaultTokenStore(app.Paths.TokenFile()).Save(email, tok); err != nil {
				return err
			}
			if err := st.EnsureAccount(ctx, email); err != nil {
				return err
			}
			fmt.Fprintf(app.Stderr, "Logged in as %s. Downloading recent mail...\n", email)
			if _, err := app.SyncNow(ctx, app.progressPrinter(), email); err != nil {
				return err
			}
			fmt.Fprintln(app.Stderr, "\nDone. Run climail to read your mail.")
			return nil
		},
	}
}

// progressPrinter shows a first sync's download on one updating line.
func (a *App) progressPrinter() func(account string, done, total int) {
	return func(account string, done, total int) {
		fmt.Fprintf(a.Stderr, "\r%s: %d/%d conversations", account, done, total)
	}
}

func newAuthStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show accounts and their sync status",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			accounts, err := app.Accounts(ctx)
			if err != nil {
				return err
			}
			svc, err := app.Service()
			if err != nil {
				return err
			}
			for _, account := range accounts {
				st, err := svc.Status(ctx, account)
				if err != nil {
					return err
				}
				last := "never synced"
				if !st.LastSync.IsZero() {
					last = "synced " + app.Now().Sub(st.LastSync).Round(time.Second).String() + " ago"
				}
				fmt.Fprintf(app.Stdout, "%s\t%s\t%d change(s) pending\n", account, last, st.Pending)
			}
			return nil
		},
	}
}

func newAuthLogoutCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "logout ACCOUNT",
		Short: "Remove an account: forget its login and its local mail (changes not yet sent are lost)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			account, err := app.Account(ctx, args[0])
			if err != nil {
				return err
			}
			if err := auth.DefaultTokenStore(app.Paths.TokenFile()).Delete(account); err != nil {
				return err
			}
			if err := app.store.DeleteAccount(ctx, account); err != nil {
				return err
			}
			fmt.Fprintf(app.Stderr, "Logged out %s.\n", account)
			return nil
		},
	}
}
