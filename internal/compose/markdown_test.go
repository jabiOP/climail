package compose_test

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jabiOP/climail/internal/compose"
)

// parts reads a built message's text parts by type, and its attachments' names.
func parts(t *testing.T, raw []byte) (map[string]string, []string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	var files []string
	var walk func(ctype string, body io.Reader)
	walk = func(ctype string, body io.Reader) {
		media, params, _ := mime.ParseMediaType(ctype)
		if !strings.HasPrefix(media, "multipart/") {
			data, _ := io.ReadAll(body)
			texts[media] = string(data)
			return
		}
		r := multipart.NewReader(body, params["boundary"])
		for {
			p, err := r.NextPart()
			if err != nil {
				return
			}
			if p.FileName() != "" {
				files = append(files, p.FileName())
				continue
			}
			walk(p.Header.Get("Content-Type"), p)
		}
	}
	walk(msg.Header.Get("Content-Type"), msg.Body)
	return texts, files
}

func TestBuild_SendsMarkdownAsFormattedMailWithAPlainVersion(t *testing.T) {
	body := "Hi **Maya**, see [the plan](https://x.example/plan) and file_name_here.\nSecond line\n\n- one\n- two\n\n" +
		"Literal <b>tags</b> stay text. Write to tom@x.example.\n\n<https://x.example> first.\n\nOn Thu, Maya <maya@x.example> wrote:\n> Quoted *line*\n"
	raw, err := compose.Draft{Account: me, To: "a@x.com", Subject: "Plan", Body: body}.Build(now)
	if err != nil {
		t.Fatal(err)
	}

	texts, _ := parts(t, raw)
	if texts["text/plain"] != strings.ReplaceAll(body, "\n", "\r\n") {
		t.Errorf("plain part:\n%q", texts["text/plain"])
	}
	html := texts["text/html"]
	for _, want := range []string{
		`<div dir="ltr">`,
		"<strong>Maya</strong>", `<a href="https://x.example/plan">the plan</a>`, "file_name_here",
		"Second line", "<br", // a line break stays one, as typed
		"<li>one</li>", "Literal &lt;b&gt;tags&lt;/b&gt; stay text.",
		`Write to <a href="mailto:tom@x.example">tom@x.example</a>.`,
		`<p>&lt;<a href="https://x.example">https://x.example</a>&gt; first.`,
		`Maya &lt;<a href="mailto:maya@x.example">maya@x.example</a>&gt; wrote:`,
		`<blockquote class="gmail_quote" style="margin:0px 0px 0px 0.8ex;border-left:1px solid rgb(204,204,204);padding-left:1ex">`,
		"<em>line</em>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "style=\"font") || strings.Contains(html, "<font") {
		t.Errorf("HTML sets a font; Gmail's default should apply:\n%s", html)
	}
}

func TestBuild_AttachmentsGoBesideTheFormattedBody(t *testing.T) {
	raw, err := compose.Draft{Account: me, To: "a@x.com", Body: "**hi**\n", Attach: []string{writeFile(t, "a.txt", "x")}}.Build(now)
	if err != nil {
		t.Fatal(err)
	}
	texts, files := parts(t, raw)
	if !strings.Contains(texts["text/html"], "<strong>hi</strong>") || texts["text/plain"] != "**hi**\r\n" || len(files) != 1 {
		t.Errorf("texts %v, files %v", texts, files)
	}
}

func TestBuild_PlainSendsTheTextAsIs(t *testing.T) {
	raw, err := compose.Draft{Account: me, To: "a@x.com", Body: "# not a heading\n", Plain: true}.Build(now)
	if err != nil {
		t.Fatal(err)
	}
	texts, _ := parts(t, raw)
	if len(texts) != 1 || texts["text/plain"] != "# not a heading\r\n" {
		t.Errorf("texts %v", texts)
	}
}

func writeFile(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
