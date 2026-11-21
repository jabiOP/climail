package gmail_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/option"

	"github.com/jabiOP/climail/internal/gmail"
	"github.com/jabiOP/climail/internal/model"
)

// serve starts a fake Gmail endpoint that answers with handler.
func serve(t *testing.T, handler http.HandlerFunc) *gmail.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := gmail.NewClient(context.Background(), "me@x.com", srv.Client(), option.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }

func TestHistory_CollectsAllPages(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			writeJSON(w, map[string]any{"historyId": "10", "nextPageToken": "p2", "history": []any{
				map[string]any{"messagesAdded": []any{map[string]any{"message": map[string]any{"id": "m1", "threadId": "t1"}}}},
				map[string]any{"labelsAdded": []any{map[string]any{"message": map[string]any{"id": "m2"}}}},
			}})
			return
		}
		writeJSON(w, map[string]any{"historyId": "12", "history": []any{
			map[string]any{"messagesAdded": []any{map[string]any{"message": map[string]any{"id": "m3", "threadId": "t1"}}}},
			map[string]any{"messagesDeleted": []any{map[string]any{"message": map[string]any{"id": "m4"}}}},
			map[string]any{"labelsRemoved": []any{map[string]any{"message": map[string]any{"id": "m2"}}}},
		}})
	})

	ch, latest, err := c.History(context.Background(), 5)

	if err != nil {
		t.Fatal(err)
	}
	want := model.Changes{Threads: []string{"t1"}, Deleted: []string{"m4"}, Relabeled: []string{"m2"}}
	if diff := cmp.Diff(want, ch); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if latest != 12 {
		t.Errorf("latest = %d, want 12", latest)
	}
}

func TestHistory_ExpiredStart(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":404,"message":"Requested entity was not found."}}`, http.StatusNotFound)
	})

	_, _, err := c.History(context.Background(), 1)

	if !errors.Is(err, gmail.ErrHistoryExpired) {
		t.Errorf("err = %v, want ErrHistoryExpired", err)
	}
}

func TestThreadIDs_PagesUpToMax(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			writeJSON(w, map[string]any{"nextPageToken": "p2", "threads": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}})
			return
		}
		writeJSON(w, map[string]any{"nextPageToken": "p3", "threads": []any{map[string]any{"id": "c"}}})
	})

	got, err := c.ThreadIDs(context.Background(), "", 3)

	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"a", "b", "c"}, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestErrors_Classified(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   error
	}{
		{404, `{"error":{"code":404,"message":"gone"}}`, gmail.ErrNotFound},
		{400, `{"error":{"code":400,"message":"bad"}}`, gmail.ErrPermanent},
		{401, `{"error":{"code":401,"message":"no"}}`, gmail.ErrUnauthorized},
		{403, `{"error":{"code":403,"message":"slow down","errors":[{"reason":"rateLimitExceeded"}]}}`, nil},
		{500, `{"error":{"code":500,"message":"oops"}}`, nil},
	}
	for _, tt := range tests {
		c := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, tt.body, tt.status) })

		err := c.Modify(context.Background(), "m1", nil, []string{"INBOX"})

		for _, e := range []error{gmail.ErrNotFound, gmail.ErrPermanent, gmail.ErrUnauthorized} {
			if errors.Is(err, e) != (e == tt.want) {
				t.Errorf("%d: err = %v; is %v = %v", tt.status, err, e, errors.Is(err, e))
			}
		}
		if err == nil {
			t.Errorf("%d: err = nil", tt.status)
		}
	}
}

func TestAttachmentData_FetchesByPosition(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/attachments/fresh-id"):
			writeJSON(w, map[string]any{"data": b64("second file")})
		default:
			writeJSON(w, map[string]any{"id": "m1", "payload": map[string]any{"mimeType": "multipart/mixed", "parts": []any{
				map[string]any{"mimeType": "text/plain", "body": map[string]any{"data": b64("hi")}},
				map[string]any{"filename": "a.txt", "body": map[string]any{"data": b64("first file")}},
				map[string]any{"filename": "b.pdf", "body": map[string]any{"attachmentId": "fresh-id", "size": 11}},
			}}})
		}
	})

	first, err1 := c.AttachmentData(context.Background(), "m1", 0)
	second, err2 := c.AttachmentData(context.Background(), "m1", 1)
	_, err3 := c.AttachmentData(context.Background(), "m1", 2)

	if string(first) != "first file" || string(second) != "second file" || err1 != nil || err2 != nil {
		t.Errorf("got %q (%v), %q (%v)", first, err1, second, err2)
	}
	if !errors.Is(err3, gmail.ErrNotFound) {
		t.Errorf("out of range: err = %v", err3)
	}
}
