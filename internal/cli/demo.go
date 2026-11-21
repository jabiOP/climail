package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/config"
	"github.com/jabiOP/climail/internal/demo"
	"github.com/jabiOP/climail/internal/gmail/gmailtest"
	"github.com/jabiOP/climail/internal/syncer"
)

func newDemoCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "demo",
		Short: "Try climail on a made-up mailbox (two accounts), without a Google login",
		Long: "Open the TUI on a made-up mailbox with two accounts. Nothing touches your real mail or settings:\n" +
			"the demo lives in a temporary folder, removed on exit, and talks to an in-memory Gmail.",
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dir, err := os.MkdirTemp("", "climail-demo-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			fakes := map[string]*gmailtest.Fake{}
			for _, mb := range demo.Mailboxes(app.Now()) {
				f := gmailtest.New(mb.Account)
				f.SetLabels(mb.Labels...)
				f.Deliver(mb.Messages...)
				fakes[mb.Account] = f
			}
			d := &App{
				Paths: config.Paths{ConfigDir: dir, DataDir: dir}, Config: app.Config, Now: app.Now,
				Stdin: app.Stdin, Stdout: app.Stdout, Stderr: app.Stderr, IsTTY: app.IsTTY,
				Remote: func(_ context.Context, account string) (syncer.Remote, error) { return fakes[account], nil },
			}
			defer d.Close()
			st, err := d.Store()
			if err != nil {
				return err
			}
			for _, mb := range demo.Mailboxes(app.Now()) {
				if err := st.EnsureAccount(ctx, mb.Account); err != nil {
					return err
				}
			}
			if _, err := d.SyncNow(ctx, nil); err != nil {
				return err
			}
			if !d.IsTTY {
				return listDemo(ctx, d)
			}
			return NewRootCmd(d).RunE(cmd, nil)
		},
	}
}

// listDemo lists the demo inbox when not on a terminal (e.g. in tests).
func listDemo(ctx context.Context, d *App) error {
	if Run(ctx, d, []string{"list", "--offline"}) != exitOK {
		return silentError{exitError}
	}
	return nil
}
