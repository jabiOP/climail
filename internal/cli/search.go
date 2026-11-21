package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jabiOP/climail/internal/output"
	"github.com/jabiOP/climail/internal/store"
)

// searchMax is how many conversations a Gmail search brings back.
const searchMax = 100

// Search asks Gmail, fetches matches not cached yet, and returns their IDs in Gmail's order.
func (a *App) Search(ctx context.Context, account, query string) ([]string, error) {
	e, err := a.engine(ctx, account)
	if err != nil {
		return nil, err
	}
	return e.Search(ctx, query, searchMax)
}

func newSearchCmd(app *App) *cobra.Command {
	var account string
	var limit int
	var offline, json, tsv bool
	cmd := &cobra.Command{
		Use:   "search QUERY...",
		Short: "Search all your mail on Gmail, with Gmail's search syntax (from:, has:attachment, older_than:…)",
		Long: `Search all your mail on Gmail, with Gmail's search syntax, e.g.
  climail search from:maya has:attachment
  climail search "quarterly report" older_than:1y
Mail not in the local cache is downloaded. With --offline, the local cache is searched
instead, for messages containing all the words.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError{errors.New("give something to search for")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mode, err := output.ChooseMode(json, tsv, app.IsTTY)
			if err != nil {
				return usageError{err}
			}
			acct, err := app.Account(ctx, account)
			if err != nil {
				return err
			}
			svc, err := app.Service()
			if err != nil {
				return err
			}
			query := strings.Join(args, " ")
			if offline {
				threads, err := svc.Threads(ctx, acct, store.View{Search: query}, limit, 0)
				if err != nil {
					return err
				}
				return output.Threads(app.Stdout, mode, threads, app.Now())
			}
			ids, err := app.Search(ctx, acct, query)
			if err != nil {
				return err
			}
			threads, err := svc.ThreadsByID(ctx, acct, ids[:min(limit, len(ids))])
			if err != nil {
				return err
			}
			return output.Threads(app.Stdout, mode, threads, app.Now())
		},
	}
	addAccountFlag(cmd, &account)
	cmd.Flags().BoolVar(&offline, "offline", false, "search the local cache instead (all the words, any case)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 25, "how many conversations")
	cmd.Flags().BoolVar(&json, "json", false, "print JSON Lines")
	cmd.Flags().BoolVar(&tsv, "tsv", false, "print TSV: id, date, unread, from, subject, messages")
	return cmd
}
