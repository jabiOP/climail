package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/browser"
	"github.com/jabiOP/climail/internal/download"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
)

// fetcher downloads an account's attachments from Gmail.
func (a *App) fetcher(ctx context.Context, account string) download.Fetch {
	return func(ctx context.Context, messageID string, index int) ([]byte, error) {
		remote, err := a.Remote(ctx, account)
		if err != nil {
			return nil, err
		}
		return remote.AttachmentData(ctx, messageID, index)
	}
}

// Download saves attachments into dir (the configured download folder if empty).
func (a *App) Download(ctx context.Context, account string, refs []model.AttachmentRef, dir string) ([]string, error) {
	if dir == "" {
		dir = a.Config.DownloadDir
	}
	return download.All(ctx, a.fetcher(ctx, account), refs, dir)
}

// ViewHTML fetches a message's HTML from Gmail and writes it as a private
// page, with scripts blocked, for the browser; it returns the page's path.
func (a *App) ViewHTML(ctx context.Context, account, messageID string) (string, error) {
	remote, err := a.Remote(ctx, account)
	if err != nil {
		return "", err
	}
	html, err := remote.MessageHTML(ctx, messageID)
	if err != nil {
		return "", err
	}
	if html == "" {
		return "", errors.New("this message is plain text: it reads the same here")
	}
	dir := a.Paths.OpenDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "message-"+filepath.Base(messageID)+".html")
	return path, os.WriteFile(path, []byte(browser.Page(html)), 0o600)
}

// forwardFiles downloads a message's attachments to send along with a
// forward, each forward in its own folder.
func (a *App) forwardFiles(ctx context.Context, account string, t model.Thread) ([]string, error) {
	var refs []model.AttachmentRef
	for _, r := range t.Attachments() {
		if r.MessageID == t.Latest().ID {
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	dir := filepath.Join(a.Paths.OpenDir(), "forward-"+a.Now().Format("20060102-150405.000"))
	return a.Download(ctx, account, refs, dir)
}

func newAttachmentsCmd(app *App) *cobra.Command {
	var account string
	var offline bool
	cmd := &cobra.Command{
		Use:   "attachments ID",
		Short: "List a conversation's attachments (number, name, size, sender)",
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
			from := map[string]string{}
			for _, m := range t.Messages {
				from[m.ID] = m.From.Short()
			}
			for i, r := range t.Attachments() {
				fmt.Fprintf(app.Stdout, "%d\t%s\t%s\t%s\n", i+1, r.Filename, output.Size(r.Size), from[r.MessageID])
			}
			return nil
		},
	}
	addAccountFlag(cmd, &account)
	addOfflineFlag(cmd, &offline)
	return cmd
}

func newDownloadCmd(app *App) *cobra.Command {
	var account, dir string
	var open bool
	cmd := &cobra.Command{
		Use:   "download ID [N...]",
		Short: "Save a conversation's attachments (all, or the numbers `climail attachments` shows); prints their paths",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError{fmt.Errorf("give a conversation ID")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			acct, svc, err := app.reader(ctx, account, true)
			if err != nil {
				return err
			}
			t, err := findThread(ctx, svc, acct, args[0])
			if err != nil {
				return err
			}
			all := t.Attachments()
			refs := all
			if len(args) > 1 {
				refs = nil
				for _, a := range args[1:] {
					n, err := strconv.Atoi(a)
					if err != nil || n < 1 || n > len(all) {
						return usageError{fmt.Errorf("no attachment %s: the conversation has %d", a, len(all))}
					}
					refs = append(refs, all[n-1])
				}
			}
			if len(refs) == 0 {
				return fmt.Errorf("the conversation has no attachments")
			}
			paths, err := app.Download(ctx, acct, refs, dir)
			for _, p := range paths {
				fmt.Fprintln(app.Stdout, p)
				if open && app.OpenURL != nil {
					if oerr := app.OpenURL(p); oerr != nil {
						fmt.Fprintf(app.Stderr, "climail: couldn't open %s: %v\n", p, oerr)
					}
				}
			}
			return err
		},
	}
	addAccountFlag(cmd, &account)
	cmd.Flags().StringVarP(&dir, "output", "o", "", "folder to save into (default: config download_dir, ~/Downloads)")
	cmd.Flags().BoolVar(&open, "open", false, "open each file once saved")
	return cmd
}
