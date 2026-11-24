// Package gmail adapts the Gmail API v1 to climail's model. Gmail's types
// never leave this package.
package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
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
	p, err := do(ctx, c.svc.Users.GetProfile("me").Context(ctx).Do)
	if err != nil {
		return "", 0, mapErr(err)
	}
	return p.EmailAddress, p.HistoryId, nil
}

func (c *Client) Labels(ctx context.Context) ([]model.Label, error) {
	resp, err := do(ctx, c.svc.Users.Labels.List("me").Context(ctx).Do)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]model.Label, len(resp.Labels))
	for i, l := range resp.Labels {
		out[i] = LabelFromAPI(c.account, l)
	}
	return out, nil
}

// ThreadIDs lists up to max thread IDs matching query (Gmail's search
// syntax), newest first; query "" lists all, outside spam and trash.
func (c *Client) ThreadIDs(ctx context.Context, query string, max int) ([]string, error) {
	var ids []string
	call := c.svc.Users.Threads.List("me").Context(ctx)
	if query != "" {
		call.Q(query)
	}
	for page := ""; len(ids) < max; {
		resp, err := do(ctx, call.MaxResults(int64(min(max-len(ids), 500))).PageToken(page).Do)
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
	t, err := do(ctx, c.svc.Users.Threads.Get("me", id).Format("full").Context(ctx).Do)
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
	m, err := do(ctx, c.svc.Users.Messages.Get("me", id).Format("minimal").Context(ctx).Do)
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
		resp, err := do(ctx, call.PageToken(page).Do)
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
	_, err := do(ctx, c.svc.Users.Messages.Modify("me", id, req).Context(ctx).Do)
	return mapErr(err)
}

func (c *Client) Trash(ctx context.Context, id string) error {
	_, err := do(ctx, c.svc.Users.Messages.Trash("me", id).Context(ctx).Do)
	return mapErr(err)
}

func (c *Client) Untrash(ctx context.Context, id string) error {
	_, err := do(ctx, c.svc.Users.Messages.Untrash("me", id).Context(ctx).Do)
	return mapErr(err)
}

// Send sends a raw RFC 5322 message; with threadID, into that conversation.
func (c *Client) Send(ctx context.Context, raw []byte, threadID string) error {
	m := &gmailapi.Message{Raw: base64.URLEncoding.EncodeToString(raw), ThreadId: threadID}
	_, err := do(ctx, c.svc.Users.Messages.Send("me", m).Context(ctx).Do)
	return mapErr(err)
}

// SendAsName is the display name set in Gmail's "Send mail as" settings for
// the account's own address; "" when none is set.
func (c *Client) SendAsName(ctx context.Context) (string, error) {
	resp, err := do(ctx, c.svc.Users.Settings.SendAs.List("me").Context(ctx).Do)
	if err != nil {
		return "", mapErr(err)
	}
	name := ""
	for _, s := range resp.SendAs {
		if strings.EqualFold(s.SendAsEmail, c.account) {
			return s.DisplayName, nil
		}
		if s.IsPrimary {
			name = s.DisplayName
		}
	}
	return name, nil
}

// SaveDraft saves raw in Drafts, replacing the draft whose message is
// replaces; when that one is gone (sent or deleted elsewhere), as a new draft.
func (c *Client) SaveDraft(ctx context.Context, replaces string, raw []byte, threadID string) error {
	m := &gmailapi.Message{Raw: base64.URLEncoding.EncodeToString(raw), ThreadId: threadID}
	if replaces != "" {
		id, err := c.draftID(ctx, replaces)
		if err == nil {
			_, err = do(ctx, c.svc.Users.Drafts.Update("me", id, &gmailapi.Draft{Id: id, Message: m}).Context(ctx).Do)
			return mapErr(err)
		}
		if !isNotFound(err) {
			return err
		}
	}
	_, err := do(ctx, c.svc.Users.Drafts.Create("me", &gmailapi.Draft{Message: m}).Context(ctx).Do)
	return mapErr(err)
}

// DeleteDraft deletes the draft whose message is messageID; nil if it's gone.
func (c *Client) DeleteDraft(ctx context.Context, messageID string) error {
	id, err := c.draftID(ctx, messageID)
	if err == nil {
		err = mapErr(retry(ctx, c.svc.Users.Drafts.Delete("me", id).Context(ctx).Do))
	}
	if isNotFound(err) {
		return nil
	}
	return err
}

// draftID finds the draft holding a message: Gmail addresses drafts by
// their own IDs, which climail doesn't keep.
func (c *Client) draftID(ctx context.Context, messageID string) (string, error) {
	call := c.svc.Users.Drafts.List("me").MaxResults(500).Context(ctx)
	for page := ""; ; {
		resp, err := do(ctx, call.PageToken(page).Do)
		if err != nil {
			return "", mapErr(err)
		}
		for _, d := range resp.Drafts {
			if d.Message != nil && d.Message.Id == messageID {
				return d.Id, nil
			}
		}
		if page = resp.NextPageToken; page == "" {
			return "", fmt.Errorf("%w: draft of message %s", ErrNotFound, messageID)
		}
	}
}

// AttachmentData downloads a message's index-th attachment (in the order of
// model.Message.Attachments). The message is fetched again first: Gmail's
// attachment IDs change from one fetch to the next.
func (c *Client) AttachmentData(ctx context.Context, messageID string, index int) ([]byte, error) {
	m, err := do(ctx, c.svc.Users.Messages.Get("me", messageID).Format("full").Context(ctx).Do)
	if err != nil {
		return nil, mapErr(err)
	}
	parts := attachmentParts(m.Payload)
	if index < 0 || index >= len(parts) {
		return nil, fmt.Errorf("%w: attachment %d of message %s", ErrNotFound, index+1, messageID)
	}
	body := parts[index].Body
	if body == nil {
		return nil, nil
	}
	if body.AttachmentId == "" { // small ones come inline
		return decodeBase64(body.Data)
	}
	a, err := do(ctx, c.svc.Users.Messages.Attachments.Get("me", messageID, body.AttachmentId).Context(ctx).Do)
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeBase64(a.Data)
}

// RateLimitWait is how long the first retry waits when Gmail says too many
// requests; each next one waits twice as long. Tests lower it.
var RateLimitWait = time.Second

// rateLimitRetries: 1+2+4+8+16+32 s, past the minute Gmail's per-user quota counts over.
const rateLimitRetries = 6

// do makes a Gmail call, retrying it while Gmail says too many requests.
func do[T any](ctx context.Context, call func(...googleapi.CallOption) (T, error)) (T, error) {
	var out T
	err := retry(ctx, func(opts ...googleapi.CallOption) error {
		var err error
		out, err = call(opts...)
		return err
	})
	return out, err
}

func retry(ctx context.Context, call func(...googleapi.CallOption) error) error {
	wait := RateLimitWait
	for attempt := 0; ; attempt++ {
		err := call()
		if err == nil || attempt == rateLimitRetries || !rateLimited(err) {
			return err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return err
		}
		wait *= 2
	}
}
