// Package browser opens URLs in the user's web browser.
package browser

import (
	"os/exec"
	"runtime"
	"strings"
)

// Open tries the platform's opener (xdg-open on Linux, open on macOS).
// Callers should also show the URL, in case it fails.
func Open(url string) error {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	return exec.Command(cmd, url).Start()
}

// policy keeps a mail's page to what mail may do: no scripts, frames or forms;
// pictures, styles and fonts load as the mail asks (as in Gmail).
const policy = `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; ` +
	`img-src * data:; style-src * 'unsafe-inline'; font-src * data:; form-action 'none'; base-uri 'none'">`

// Page makes an HTML mail safe to open from a file: the policy goes at the
// top of its head, so it holds before anything else is read, and so does
// the charset (the text was already decoded to UTF-8).
func Page(html string) string {
	lower := strings.ToLower(html)
	at := 0
	for _, tag := range []string{"<head>", "<head ", "<!doctype"} {
		if i := strings.Index(lower, tag); i >= 0 {
			if end := strings.IndexByte(lower[i:], '>'); end >= 0 {
				at = i + end + 1
				break
			}
		}
	}
	return html[:at] + policy + html[at:]
}
