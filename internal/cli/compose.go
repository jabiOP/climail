package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/compose"
)

// draftFlags are the headers a command line can fill in.
type draftFlags struct {
	to, cc, bcc, subject string
	attach               []string
}

func addDraftFlags(cmd *cobra.Command, f *draftFlags) {
	cmd.Flags().StringVar(&f.to, "to", "", "recipients, comma-separated")
	cmd.Flags().StringVar(&f.cc, "cc", "", "cc recipients")
	cmd.Flags().StringVar(&f.bcc, "bcc", "", "bcc recipients")
	cmd.Flags().StringVarP(&f.subject, "subject", "s", "", "subject")
	cmd.Flags().StringArrayVar(&f.attach, "attach", nil, "a file to attach (repeat for more)")
}

// over fills in the draft from the flags that were given.
func (f draftFlags) over(d compose.Draft) compose.Draft {
	for _, v := range []struct {
		flag  string
		field *string
	}{{f.to, &d.To}, {f.cc, &d.Cc}, {f.bcc, &d.Bcc}, {f.subject, &d.Subject}} {
		if v.flag != "" {
			*v.field = v.flag
		}
	}
	d.Attach = append(d.Attach, f.attach...)
	return d
}

func newComposeCmd(app *App) *cobra.Command {
	var account string
	var f draftFlags
	cmd := &cobra.Command{
		Use:   "compose",
		Short: "Write a new mail in your editor ($VISUAL or $EDITOR), then send it",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			acct, err := app.Account(cmd.Context(), account)
			if err != nil {
				return err
			}
			return app.editAndSend(cmd.Context(), f.over(compose.Draft{Account: acct, Body: "\n"}))
		},
	}
	addAccountFlag(cmd, &account)
	addDraftFlags(cmd, &f)
	return cmd
}

func newReplyCmd(app *App) *cobra.Command {
	var account string
	var all bool
	cmd := &cobra.Command{
		Use:   "reply ID",
		Short: "Reply to a conversation's latest message in your editor",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct, svc, err := app.reader(ctx, account, false)
			if err != nil {
				return err
			}
			t, err := findThread(ctx, svc, acct, args[0])
			if err != nil {
				return err
			}
			return app.editAndSend(ctx, compose.Reply(t, acct, all, app.Now()))
		},
	}
	addAccountFlag(cmd, &account)
	cmd.Flags().BoolVar(&all, "all", false, "reply to everyone on the message")
	return cmd
}

func newForwardCmd(app *App) *cobra.Command {
	var account string
	var f draftFlags
	cmd := &cobra.Command{
		Use:   "forward ID",
		Short: "Forward a conversation's latest message in your editor",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct, svc, err := app.reader(ctx, account, false)
			if err != nil {
				return err
			}
			t, err := findThread(ctx, svc, acct, args[0])
			if err != nil {
				return err
			}
			return app.editAndSend(ctx, f.over(compose.Forward(t, acct, nil, app.Now())))
		},
	}
	addAccountFlag(cmd, &account)
	addDraftFlags(cmd, &f)
	return cmd
}

// editAndSend opens the draft in the editor, then asks whether to send it.
// A draft not sent stays on disk, and the path is printed.
func (a *App) editAndSend(ctx context.Context, base compose.Draft) error {
	path, err := compose.WriteDraft(a.Paths.DraftDir(), base, a.Now())
	if err != nil {
		return err
	}
	in := bufio.NewReader(a.Stdin)
	ask := func(question string) string {
		fmt.Fprint(a.Stderr, question)
		answer, _ := in.ReadString('\n') // EOF: no answer, so nothing happens
		return strings.ToLower(strings.TrimSpace(answer))
	}
	kept := func() error {
		fmt.Fprintf(a.Stderr, "Not sent. The draft is in %s\n", path)
		return nil
	}
	for {
		if err := a.Edit(path); err != nil {
			return fmt.Errorf("editor: %w (set $EDITOR to the one you use)", err)
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(text) == base.Text() {
			os.Remove(path)
			fmt.Fprintln(a.Stderr, "Not sent: the draft wasn't changed.")
			return nil
		}
		d, err := compose.Parse(string(text), base)
		if err == nil {
			err = d.Check()
		}
		if err != nil {
			if ask(fmt.Sprintf("climail: %v\n[e]dit or [q]uit? ", err)) == "e" {
				continue
			}
			return kept()
		}
		switch ask(fmt.Sprintf("Send %s? [y]es, [e]dit, [q]uit: ", d.Summary())) {
		case "y", "yes":
			return a.queue(ctx, d, path)
		case "e":
			continue
		}
		return kept()
	}
}

// queue queues a draft and sends it right away if possible.
func (a *App) queue(ctx context.Context, d compose.Draft, path string) error {
	svc, err := a.Service()
	if err != nil {
		return err
	}
	if err := svc.Send(ctx, d, a.Now()); err != nil {
		return err
	}
	if path != "" {
		os.Remove(path)
	}
	fmt.Fprintf(a.Stderr, "Sending %s.\n", d.Summary())
	a.PushAfterWrite(ctx, d.Account, false)
	return nil
}

func newSendCmd(app *App) *cobra.Command {
	var account string
	var f draftFlags
	var offline bool
	cmd := &cobra.Command{
		Use:   "send --to ADDRESSES [--subject S] [--attach FILE]... < BODY",
		Short: "Send a mail without an editor: the body is read from stdin (for scripts)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct, err := app.Account(ctx, account)
			if err != nil {
				return err
			}
			body, err := io.ReadAll(app.Stdin)
			if err != nil {
				return err
			}
			d := f.over(compose.Draft{Account: acct, Body: string(body)})
			if err := d.Check(); err != nil {
				return usageError{err}
			}
			if offline {
				svc, err := app.Service()
				if err != nil {
					return err
				}
				if err := svc.Send(ctx, d, app.Now()); err != nil {
					return err
				}
				fmt.Fprintf(app.Stderr, "Queued %s; it goes with the next sync.\n", d.Summary())
				return nil
			}
			return app.queue(ctx, d, "")
		},
	}
	addAccountFlag(cmd, &account)
	addDraftFlags(cmd, &f)
	cmd.Flags().BoolVar(&offline, "offline", false, "only queue it; it goes with the next sync")
	return cmd
}

func newOutboxCmd(app *App) *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:   "outbox",
		Short: "List mail waiting to be sent, and mail Gmail refused",
		Args:  exactArgs(0),
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
			out, err := svc.Outbox(ctx, acct)
			if err != nil {
				return err
			}
			for _, o := range out {
				state := "waiting"
				if o.Failed != "" {
					state = "refused: " + o.Failed
				}
				fmt.Fprintf(app.Stdout, "%d\t%s\t%s\n", o.ID, o.Summary, state)
			}
			return nil
		},
	}
	addAccountFlag(cmd, &account)
	for _, sub := range []struct {
		use, short string
		do         func(ctx context.Context, acct string, id int64) error
	}{
		{"retry ID", "Try sending a refused mail again", func(ctx context.Context, acct string, id int64) error {
			svc, err := app.Service()
			if err == nil {
				err = svc.RetrySend(ctx, acct, id)
			}
			if err == nil {
				app.PushAfterWrite(ctx, acct, false)
			}
			return err
		}},
		{"discard ID", "Forget a mail without sending it", func(ctx context.Context, acct string, id int64) error {
			svc, err := app.Service()
			if err == nil {
				err = svc.DiscardSend(ctx, acct, id)
			}
			return err
		}},
	} {
		var acctFlag string
		c := &cobra.Command{
			Use: sub.use, Short: sub.short, Args: exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := strconv.ParseInt(args[0], 10, 64)
				if err != nil {
					return usageError{errors.New("the ID is the number `climail outbox` shows")}
				}
				acct, err := app.Account(cmd.Context(), acctFlag)
				if err != nil {
					return err
				}
				return sub.do(cmd.Context(), acct, id)
			},
		}
		addAccountFlag(c, &acctFlag)
		cmd.AddCommand(c)
	}
	return cmd
}
