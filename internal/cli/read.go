package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
	"github.com/jabiOP/climail/internal/service"
	"github.com/jabiOP/climail/internal/store"
)

func newLabelsCmd(app *App) *cobra.Command {
	var account string
	var offline bool
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List mailboxes and labels with unread counts (TSV in a pipe: id, name, unread)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct, svc, err := app.reader(ctx, account, offline)
			if err != nil {
				return err
			}
			boxes, err := svc.Mailboxes(ctx, acct)
			if err != nil {
				return err
			}
			for _, b := range boxes {
				if !app.IsTTY {
					fmt.Fprintf(app.Stdout, "%s\t%s\t%d\n", b.ID, b.Path, b.Unread)
					continue
				}
				count := ""
				if b.Unread > 0 {
					count = fmt.Sprint(b.Unread)
				}
				fmt.Fprintf(app.Stdout, "%-30s %5s\n", strings.Repeat("  ", b.Depth)+b.Name, count)
			}
			return nil
		},
	}
	addAccountFlag(cmd, &account)
	addOfflineFlag(cmd, &offline)
	return cmd
}

// reader resolves the account and syncs it first if its cache is stale.
func (a *App) reader(ctx context.Context, name string, offline bool) (string, *service.Service, error) {
	account, err := a.Account(ctx, name)
	if err != nil {
		return "", nil, err
	}
	a.RefreshIfStale(ctx, account, offline)
	svc, err := a.Service()
	return account, svc, err
}

func newListCmd(app *App) *cobra.Command {
	var account, search string
	var limit int
	var offline, json, tsv bool
	cmd := &cobra.Command{
		Use:   "list [LABEL]",
		Short: "List conversations in a mailbox or label (default: Inbox)",
		Long: `List conversations, newest first, in a mailbox or label (default: Inbox).
LABEL is a name as ` + "`climail labels`" + ` shows it, any case: inbox, "all mail", work/clients.
In a pipe it prints TSV: id, date, unread (0/1), from, subject, messages.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageError{errors.New("give at most one label")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mode, err := output.ChooseMode(json, tsv, app.IsTTY)
			if err != nil {
				return usageError{err}
			}
			acct, svc, err := app.reader(ctx, account, offline)
			if err != nil {
				return err
			}
			label := "inbox"
			if len(args) == 1 {
				label = args[0]
			}
			box, err := svc.ResolveLabel(ctx, acct, label)
			if err != nil {
				return usageError{err}
			}
			threads, err := svc.Threads(ctx, acct, store.View{Label: box.ID, Search: search}, limit, 0)
			if err != nil {
				return err
			}
			return output.Threads(app.Stdout, mode, threads, app.Now())
		},
	}
	addAccountFlag(cmd, &account)
	addOfflineFlag(cmd, &offline)
	cmd.Flags().StringVarP(&search, "search", "q", "", "only conversations with a message containing all these words")
	cmd.Flags().IntVarP(&limit, "limit", "n", 25, "how many conversations")
	cmd.Flags().BoolVar(&json, "json", false, "print JSON Lines (one conversation per line)")
	cmd.Flags().BoolVar(&tsv, "tsv", false, "print TSV: id, date, unread, from, subject, messages")
	return cmd
}

// findThread looks a thread up wherever it is, trash and spam included.
func findThread(ctx context.Context, svc *service.Service, account, id string) (model.Thread, error) {
	for _, label := range []string{model.AllMail, model.Trash, model.Spam} {
		t, err := svc.Thread(ctx, account, id, label)
		if !errors.Is(err, store.ErrNotFound) {
			return t, err
		}
	}
	return model.Thread{}, fmt.Errorf("no conversation %q in %s (only recent mail is kept locally)", id, account)
}

func newShowCmd(app *App) *cobra.Command {
	var account string
	var offline bool
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Print a conversation as plain text",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct, svc, err := app.reader(ctx, account, offline)
			if err != nil {
				return err
			}
			t, err := findThread(ctx, svc, acct, args[0])
			if err != nil {
				return err
			}
			return output.Conversation(app.Stdout, t, app.Now())
		},
	}
	addAccountFlag(cmd, &account)
	addOfflineFlag(cmd, &offline)
	return cmd
}

func newCountCmd(app *App) *cobra.Command {
	var account string
	var offline bool
	cmd := &cobra.Command{
		Use:   "count [LABEL]",
		Short: "Print how many conversations are unread (default: Inbox, every account), for status bars",
		Long: `Print how many conversations are unread in a mailbox or label (default: Inbox), added up over
every account, or in one with -a. Without -a, LABEL is a system mailbox (inbox, starred, spam…);
with it, any label as ` + "`climail labels`" + ` shows it.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageError{errors.New("give at most one label")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct := service.AllAccounts
			var svc *service.Service
			var err error
			if account != "" {
				acct, svc, err = app.reader(ctx, account, offline)
			} else {
				svc, err = app.readAll(ctx, offline)
			}
			if err != nil {
				return err
			}
			label := "inbox"
			if len(args) == 1 {
				label = args[0]
			}
			box, err := svc.ResolveLabel(ctx, acct, label)
			if err != nil {
				return usageError{err}
			}
			fmt.Fprintln(app.Stdout, box.Unread)
			return nil
		},
	}
	cmd.Flags().StringVarP(&account, "account", "a", "", "only this account (its address, or the start of it)")
	addOfflineFlag(cmd, &offline)
	return cmd
}

// readAll syncs every account whose cache is stale.
func (a *App) readAll(ctx context.Context, offline bool) (*service.Service, error) {
	accounts, err := a.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, acct := range accounts {
		a.RefreshIfStale(ctx, acct, offline)
	}
	return a.Service()
}
