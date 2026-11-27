package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestLinksIn(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{
		{"footnotes, by number", "Read[1] and see[2]\n\n[1] https://a.example/1\n[2] https://b.example/2?x=1",
			[]string{"https://a.example/1", "https://b.example/2?x=1"}},
		{"bare URLs, once each, without the punctuation after them",
			"See https://code.example/pull/312. Or (https://x.example/a), again https://code.example/pull/312!",
			[]string{"https://code.example/pull/312", "https://x.example/a"}},
		{"only web links", "file:///etc/passwd javascript:alert(1) mailto:a@b ftp://x", nil},
		{"none", "Hello", nil},
	}
	for _, tt := range tests {
		if got := linksIn(tt.body); !slices.Equal(got, tt.want) {
			t.Errorf("%s: linksIn = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func newLinksFixture(t *testing.T, opened *[]string) *fixture {
	t.Helper()
	return newFixtureWith(t, 150, 22, Deps{OpenURL: func(u string) error { *opened = append(*opened, u); return nil }})
}

func TestLinks_OneLinkOpensAtOnce(t *testing.T) {
	var opened []string
	f := newLinksFixture(t, &opened)
	f.keys("j", "j", "j", "j", "j", "enter") // Tom: Can you review #312

	f.keys("U")

	if !slices.Equal(opened, []string{"https://code.example/climail/pull/312"}) {
		t.Errorf("opened %q", opened)
	}
}

func TestLinks_SeveralArePickedFromAList(t *testing.T) {
	var opened []string
	f := newLinksFixture(t, &opened)
	f.keys("j", "j", "j", "j", "j", "j", "j", "enter") // The Weekly Terminal, issue 142

	f.keys("U")
	if !strings.Contains(f.view(), "weekly.example/142/rendering") || len(opened) > 0 {
		t.Fatalf("no list of links (opened %q):\n%s", opened, f.view())
	}
	f.keys("j", "enter")

	if !slices.Equal(opened, []string{"https://weekly.example/142/rendering"}) {
		t.Errorf("opened %q", opened)
	}
	if f.model().links != nil {
		t.Error("the list stays open after opening a link")
	}
}

func TestLinks_ANumberPicksItsLink(t *testing.T) {
	var opened []string
	f := newLinksFixture(t, &opened)
	f.keys("j", "j", "j", "j", "j", "j", "j", "enter")

	f.keys("U", "3", "enter")

	if !slices.Equal(opened, []string{"https://weekly.example/142"}) {
		t.Errorf("opened %q", opened)
	}
}

func TestLinks_EscClosesTheList(t *testing.T) {
	var opened []string
	f := newLinksFixture(t, &opened)
	f.keys("j", "j", "j", "j", "j", "j", "j", "enter")

	f.keys("U", "esc")

	if f.model().links != nil || len(opened) > 0 {
		t.Errorf("links %q, opened %q: want closed, nothing opened", f.model().links, opened)
	}
}

func TestLinks_NoneSaysSo(t *testing.T) {
	var opened []string
	f := newLinksFixture(t, &opened)
	f.keys("enter") // Maya's launch plan

	f.keys("U")

	if !strings.Contains(f.view(), "no links") {
		t.Errorf("no message:\n%s", f.view())
	}
}
