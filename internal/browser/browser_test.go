package browser_test

import (
	"strings"
	"testing"

	"github.com/jabiOP/climail/internal/browser"
)

func TestPage_BlocksScriptsAndSetsTheCharsetFirst(t *testing.T) {
	tests := []struct{ name, html, before string }{
		{"in its head", `<!DOCTYPE html><html><head lang="en"><meta charset="iso-8859-1"><title>x</title></head><body>Hi</body></html>`,
			`<meta charset="iso-8859-1">`},
		{"in its HEAD", `<HTML><HEAD><TITLE>x</TITLE></HEAD><BODY>Hi</BODY></HTML>`, `<TITLE>`},
		{"no head", `<div dir="ltr">Hi</div>`, `<div dir="ltr">`},
	}
	for _, tt := range tests {
		got := browser.Page(tt.html)
		csp := strings.Index(got, `http-equiv="Content-Security-Policy"`)
		charset := strings.Index(got, `<meta charset="utf-8">`)
		if csp < 0 || charset < 0 || !strings.Contains(got, "script-src 'none'") {
			t.Errorf("%s: no policy or charset:\n%s", tt.name, got)
			continue
		}
		if at := strings.Index(got, tt.before); charset > at || csp > at {
			t.Errorf("%s: the policy and charset come after %s:\n%s", tt.name, tt.before, got)
		}
		if strings.Contains(tt.html, "<!DOCTYPE") && !strings.HasPrefix(got, "<!DOCTYPE") {
			t.Errorf("%s: the doctype must stay first (else quirks mode):\n%s", tt.name, got)
		}
	}
}
