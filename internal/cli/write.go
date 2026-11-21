package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/service"
)

// newActionCmds builds one command per thread action: archive, trash…
func newActionCmds(app *App) []*cobra.Command {
	actions := []struct {
		use, short string
		action     service.Action
	}{
		{"archive", "Archive conversations (remove them from the Inbox)", service.Archive},
		{"trash", "Move conversations to the Trash", service.Trash},
		{"inbox", "Move conversations back to the Inbox (from the Trash, Spam or archive)", service.MoveToInbox},
		{"read", "Mark conversations as read", service.MarkRead},
		{"unread", "Mark conversations as unread", service.MarkUnread},
		{"star", "Star conversations (their latest message, as Gmail does)", service.Star},
		{"unstar", "Remove the stars from conversations", service.Unstar},
	}
	var cmds []*cobra.Command
	for _, a := range actions {
		var account string
		var offline bool
		cmd := &cobra.Command{
			Use:   a.use + " ID... | -",
			Short: a.short,
			Long:  a.short + ".\nWith -, the IDs are read from stdin: the first word of each line, so `climail list` output can be piped in.",
			Args: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return usageError{errors.New("give conversation IDs, or - to read them from stdin")}
				}
				return nil
			},
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				acct, err := app.Account(ctx, account)
				if err != nil {
					return err
				}
				svc, err := app.Service()
				if err != nil {
					return err
				}
				if len(args) == 1 && args[0] == "-" {
					args = idsFromStdin(app)
				}
				var threads []model.Thread
				for _, id := range args {
					t, err := findThread(ctx, svc, acct, id)
					if err != nil {
						return err
					}
					threads = append(threads, t)
				}
				if _, err := svc.Apply(ctx, acct, threads, a.action); err != nil {
					return err
				}
				fmt.Fprintf(app.Stderr, "%s %s.\n", a.action.Done, conversations(len(threads)))
				app.PushAfterWrite(ctx, acct, offline)
				return nil
			},
		}
		addAccountFlag(cmd, &account)
		addOfflineFlag(cmd, &offline)
		cmds = append(cmds, cmd)
	}
	return cmds
}

func conversations(n int) string {
	if n == 1 {
		return "1 conversation"
	}
	return fmt.Sprintf("%d conversations", n)
}

// idsFromStdin reads the first word of each non-empty line.
func idsFromStdin(app *App) []string {
	var ids []string
	sc := bufio.NewScanner(app.Stdin)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 0 {
			ids = append(ids, f[0])
		}
	}
	return ids
}
