package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jabiOP/climail/internal/auth"
	"github.com/jabiOP/climail/internal/browser"
	"github.com/jabiOP/climail/internal/config"
	"github.com/jabiOP/climail/internal/gmail"
	"github.com/jabiOP/climail/internal/syncer"
)

// Version is set by main.
var Version = "dev"

// Main is the real entry point: wires the App and runs the command line.
func Main() int {
	app, err := NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, "climail:", err)
		return exitError
	}
	defer app.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, app, os.Args[1:])
}

// Run executes one command line against app and returns the exit code.
// Main and the tests both go through here.
func Run(ctx context.Context, app *App, args []string) int {
	root := NewRootCmd(app)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil && err.Error() != "" {
		fmt.Fprintln(app.Stderr, "climail:", err)
	}
	return exitCode(err)
}

// NewApp builds the App used in production.
func NewApp() (*App, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return nil, err
	}
	return &App{
		Paths: paths, Config: cfg, Now: time.Now,
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		IsTTY:   term.IsTerminal(int(os.Stdout.Fd())),
		OpenURL: browser.Open,
		Remote: func(ctx context.Context, account string) (syncer.Remote, error) {
			oauthCfg, err := auth.LoadClient(paths.ClientFile())
			if err != nil {
				return nil, err
			}
			hc, err := auth.HTTPClient(ctx, oauthCfg, auth.DefaultTokenStore(paths.TokenFile()), account)
			if err != nil {
				return nil, err
			}
			return gmail.NewClient(ctx, account, hc)
		},
	}, nil
}

// NewRootCmd builds the command tree. Tests call it with their own App.
func NewRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "climail",
		Short:         "A fast terminal client for Gmail",
		Version:       Version,
		SilenceUsage:  true, // don't dump usage on runtime errors
		SilenceErrors: true, // Main prints errors once
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetIn(app.Stdin)
	root.SetOut(app.Stdout)
	root.SetErr(app.Stderr)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return usageError{err} })
	root.AddCommand(newAuthCmd(app), newSyncCmd(app), newLabelsCmd(app), newListCmd(app), newShowCmd(app))
	root.AddCommand(newActionCmds(app)...)
	return root
}

// exactArgs is cobra.ExactArgs, but reports a usage error (exit 2).
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

func addAccountFlag(cmd *cobra.Command, account *string) {
	cmd.Flags().StringVarP(account, "account", "a", "", "account to use (its address, or the start of it); default: the first one")
}

func addOfflineFlag(cmd *cobra.Command, offline *bool) {
	cmd.Flags().BoolVar(offline, "offline", false, "don't contact Gmail; use local data only")
}
