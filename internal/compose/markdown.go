package compose

import (
	"bytes"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// markdown renders a body as Gmail's compose window would send it: line
// breaks kept as typed, bare links linked, and no font, size or colour set,
// so the reader's Gmail shows its default formatting. HTML typed in the body
// stays text.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps(),
		renderer.WithNodeRenderers(util.Prioritized(literalHTML{}, 100))),
)

// Gmail's own quote style, so quoted mail looks as it does in Gmail.
const gmailQuote = `<blockquote class="gmail_quote" style="margin:0px 0px 0px 0.8ex;border-left:1px solid rgb(204,204,204);padding-left:1ex">`

// HTML renders a Markdown body as the HTML part of a mail.
func HTML(body string) (string, error) {
	var b bytes.Buffer
	if err := markdown.Convert([]byte(body), &b); err != nil {
		return "", err
	}
	return `<div dir="ltr">` + strings.ReplaceAll(b.String(), "<blockquote>", gmailQuote) + "</div>\n", nil
}

// literalHTML renders HTML typed in a body as the text it is, and keeps the
// brackets of "<maya@x.com>" visible around the link, as Gmail shows them.
type literalHTML struct{}

func (literalHTML) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindAutoLink, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		l := n.(*ast.AutoLink)
		label := string(l.Label(src))
		url := string(l.URL(src))
		if l.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(url), "mailto:") {
			url = "mailto:" + url
		}
		link := `<a href="` + html.EscapeString(string(util.URLEscape([]byte(url), false))) + `">` + html.EscapeString(label) + "</a>"
		if bracketed(n, src) {
			link = "&lt;" + link + "&gt;"
		}
		w.WriteString(link)
		return ast.WalkSkipChildren, nil
	})
	r.Register(ast.KindRawHTML, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segs := n.(*ast.RawHTML).Segments
			for i := 0; i < segs.Len(); i++ {
				s := segs.At(i)
				w.WriteString(html.EscapeString(string(s.Value(src))))
			}
		}
		return ast.WalkSkipChildren, nil
	})
	r.Register(ast.KindHTMLBlock, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		b := n.(*ast.HTMLBlock)
		var text strings.Builder
		for i := 0; i < b.Lines().Len(); i++ {
			s := b.Lines().At(i)
			text.Write(s.Value(src))
		}
		if b.HasClosure() {
			text.Write(b.ClosureLine.Value(src))
		}
		w.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(strings.TrimRight(text.String(), "\n")), "\n", "<br>\n") + "</p>\n")
		return ast.WalkSkipChildren, nil
	})
}

// bracketed reports whether a link was typed in angle brackets: the source
// just before it, where the text before it ends (or its paragraph starts),
// is "<". A bare link found by Linkify isn't.
func bracketed(n ast.Node, src []byte) bool {
	pos := -1
	if prev, ok := n.PreviousSibling().(*ast.Text); ok {
		pos = prev.Segment.Stop
	} else if p := n.Parent(); n.PreviousSibling() == nil && p != nil && p.Type() == ast.TypeBlock && p.Lines().Len() > 0 {
		pos = p.Lines().At(0).Start
	}
	return pos >= 0 && pos < len(src) && src[pos] == '<'
}
