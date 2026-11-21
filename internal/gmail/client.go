// Package gmail adapts the Gmail API v1 to climail's model. Gmail's types
// never leave this package.
package gmail

import (
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"github.com/jabiOP/climail/internal/model"
)

// Client talks to Gmail for one account. It satisfies syncer.Remote.
type Client struct {
	svc     *gmailapi.Service
	account string
}

// RequestTimeout bounds every request to Google, so a stalled connection
// can't hang a sync. A timeout is transient: queued changes stay queued.
var RequestTimeout = 30 * time.Second

// NewClient wraps an authorized HTTP client (see package auth). Extra options
// let tests point it at an httptest server with option.WithEndpoint.
func NewClient(ctx context.Context, account string, hc *http.Client, opts ...option.ClientOption) (*Client, error) {
	if hc.Timeout == 0 {
		limited := *hc // a copy: don't change the caller's client
		limited.Timeout = RequestTimeout
		hc = &limited
	}
	svc, err := gmailapi.NewService(ctx, append([]option.ClientOption{option.WithHTTPClient(hc)}, opts...)...)
	if err != nil {
		return nil, err
	}
	return &Client{svc: svc, account: account}, nil
}

// Profile returns the account's address and its current history ID.
func (c *Client) Profile(ctx context.Context) (string, uint64, error) {
	p, err := c.svc.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		return "", 0, mapErr(err)
	}
	return p.EmailAddress, p.HistoryId, nil
}

func (c *Client) Labels(ctx context.Context) ([]model.Label, error) {
	resp, err := c.svc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]model.Label, len(resp.Labels))
	for i, l := range resp.Labels {
		out[i] = LabelFromAPI(c.account, l)
	}
	return out, nil
}

// ThreadIDs lists up to max thread IDs, newest first, outside spam and trash.
func (c *Client) ThreadIDs(ctx context.Context, max int) ([]string, error) {
	var ids []string
	call := c.svc.Users.Threads.List("me").Context(ctx)
	for page := ""; len(ids) < max; {
		resp, err := call.MaxResults(int64(min(max-len(ids), 500))).PageToken(page).Do()
		if err != nil {
			return nil, mapErr(err)
		}
		for _, t := range resp.Threads {
			ids = append(ids, t.Id)
		}
		if page = resp.NextPageToken; page == "" {
			break
		}
	}
	return ids, nil
}

// Thread fetches every message of a thread, bodies included.
func (c *Client) Thread(ctx context.Context, id string) ([]model.Message, error) {
	t, err := c.svc.Users.Threads.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]model.Message, len(t.Messages))
	for i, m := range t.Messages {
		out[i] = FromAPI(c.account, m)
	}
	return out, nil
}

// MessageLabels fetches a message's current labels.
func (c *Client) MessageLabels(ctx context.Context, id string) ([]string, error) {
	m, err := c.svc.Users.Messages.Get("me", id).Format("minimal").Context(ctx).Do()
	if err != nil {
		return nil, mapErr(err)
	}
	return m.LabelIds, nil
}

// History lists what changed after start, and the history ID to start from
// next time. ErrHistoryExpired means start is too old: do a full sync.
func (c *Client) History(ctx context.Context, start uint64) (model.Changes, uint64, error) {
	var ch model.Changes
	latest := start
	call := c.svc.Users.History.List("me").StartHistoryId(start).MaxResults(500).Context(ctx)
	for page := ""; ; {
		resp, err := call.PageToken(page).Do()
		if err != nil {
			err = mapErr(err)
			if isNotFound(err) {
				return model.Changes{}, 0, ErrHistoryExpired
			}
			return model.Changes{}, 0, err
		}
		for _, h := range resp.History {
			for _, a := range h.MessagesAdded {
				ch.Threads = append(ch.Threads, a.Message.ThreadId)
			}
			for _, d := range h.MessagesDeleted {
				ch.Deleted = append(ch.Deleted, d.Message.Id)
			}
			for _, l := range h.LabelsAdded {
				ch.Relabeled = append(ch.Relabeled, l.Message.Id)
			}
			for _, l := range h.LabelsRemoved {
				ch.Relabeled = append(ch.Relabeled, l.Message.Id)
			}
		}
		latest = max(latest, resp.HistoryId)
		if page = resp.NextPageToken; page == "" {
			break
		}
	}
	return model.Changes{Threads: unique(ch.Threads), Deleted: unique(ch.Deleted), Relabeled: unique(ch.Relabeled)}, latest, nil
}

func unique(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

func (c *Client) Modify(ctx context.Context, id string, add, remove []string) error {
	req := &gmailapi.ModifyMessageRequest{AddLabelIds: add, RemoveLabelIds: remove}
	_, err := c.svc.Users.Messages.Modify("me", id, req).Context(ctx).Do()
	return mapErr(err)
}

func (c *Client) Trash(ctx context.Context, id string) error {
	_, err := c.svc.Users.Messages.Trash("me", id).Context(ctx).Do()
	return mapErr(err)
}

func (c *Client) Untrash(ctx context.Context, id string) error {
	_, err := c.svc.Users.Messages.Untrash("me", id).Context(ctx).Do()
	return mapErr(err)
}

// Send sends a raw RFC 5322 message; with threadID, into that conversation.
func (c *Client) Send(ctx context.Context, raw []byte, threadID string) error {
	m := &gmailapi.Message{Raw: base64.URLEncoding.EncodeToString(raw), ThreadId: threadID}
	_, err := c.svc.Users.Messages.Send("me", m).Context(ctx).Do()
	return mapErr(err)
}
