package compose

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/jabiOP/climail/internal/model"
)

// DraftBody is the Markdown to continue a draft with. One climail saved is
// as it was typed (its HTML is what that text renders to); one written in
// Gmail is converted from its HTML, and lossy says formatting is dropped.
func DraftBody(m model.Message) (body string, lossy bool) {
	if m.HTML == "" {
		return m.Body, false
	}
	if ours, err := HTML(m.Body); err == nil && strings.TrimSpace(ours) == strings.TrimSpace(m.HTML) {
		return m.Body, false
	}
	return MarkdownFromHTML(m.HTML)
}

// MarkdownFromHTML turns a mail's HTML (Gmail's compose window writes a
// <div> per line) into the Markdown climail edits: bold, italics,
// strikethrough, code, links, lists, headings and quotes. lossy reports
// formatting Markdown can't hold (fonts, colours, sizes, alignment,
// underline, tables, images), which is dropped, its text kept.
func MarkdownFromHTML(src string) (md string, lossy bool) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", true
	}
	w := &mdWriter{}
	w.children(doc)
	return w.finish(), w.lossy
}

type mdWriter struct {
	lines   []string // finished lines
	cur     strings.Builder
	started bool // cur has content
	quote   int  // blockquote depth
	list    int  // list depth
	lossy   bool
}

func (w *mdWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c)
	}
}

func (w *mdWriter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.write(escape(collapse(n.Data)), true)
		return
	case html.DocumentNode:
		w.children(n)
		return
	case html.ElementNode:
	default:
		return
	}
	tag := n.Data
	if attr(n, "style") != "" && tag != "blockquote" { // Gmail's quote style is put back on sending
		w.lossy = true
	}
	switch tag {
	case "head", "script", "style", "title":
	case "br":
		w.newline()
	case "b", "strong", "i", "em", "s", "strike", "del", "code", "a":
		w.write(w.inline(n), false)
	case "p":
		w.blank()
		w.children(n)
		w.blank()
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.blank()
		level, _ := strconv.Atoi(tag[1:])
		w.write(strings.Repeat("#", level)+" ", false)
		w.children(n)
		w.blank()
	case "ul", "ol":
		w.listBlock(n, tag == "ol")
	case "blockquote":
		w.lineBreak()
		w.quote++
		w.children(n)
		w.lineBreak()
		w.quote--
	case "pre":
		w.blank()
		w.write("```", false)
		w.newline()
		for _, line := range strings.Split(strings.TrimRight(text(n), "\n"), "\n") {
			w.write(line, false)
			w.newline()
		}
		w.write("```", false)
		w.blank()
	case "hr":
		w.blank()
		w.write("---", false)
		w.blank()
	case "img", "font", "u", "table", "center", "iframe", "video", "audio":
		w.lossy = true
		if tag != "img" {
			w.lineBreakIf(tag == "table" || tag == "center")
			w.children(n)
			w.lineBreakIf(tag == "table" || tag == "center")
		}
	case "td", "th":
		w.children(n)
		w.write(" ", false)
	default:
		block := blockTags[tag]
		w.lineBreakIf(block)
		w.children(n)
		w.lineBreakIf(block)
	}
}

var blockTags = map[string]bool{
	"div": true, "tr": true, "section": true, "article": true, "header": true, "footer": true,
	"li": true, "dl": true, "dt": true, "dd": true, "address": true, "figure": true, "main": true, "nav": true,
}

func (w *mdWriter) listBlock(n *html.Node, ordered bool) {
	if w.list == 0 {
		w.blank()
	} else {
		w.lineBreak()
	}
	w.list++
	i := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			w.node(c)
			continue
		}
		i++
		w.lineBreak()
		marker := "- "
		if ordered {
			marker = strconv.Itoa(i) + ". "
		}
		w.write(strings.Repeat("  ", w.list-1)+marker, false)
		w.children(c)
	}
	w.list--
	if w.list == 0 {
		w.blank()
	} else {
		w.lineBreak()
	}
}

// inline renders an inline element as Markdown, its text escaped.
func (w *mdWriter) inline(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode:
			b.WriteString(escape(collapse(c.Data)))
		case c.Type != html.ElementNode:
		case c.Data == "br":
			b.WriteString("\n")
		case c.Data == "img":
			w.lossy = true
		default:
			if attr(c, "style") != "" || c.Data == "font" || c.Data == "u" {
				w.lossy = true
			}
			b.WriteString(w.inline(c))
		}
	}
	inner := b.String()
	marker := map[string]string{"b": "**", "strong": "**", "i": "*", "em": "*", "s": "~~", "strike": "~~", "del": "~~",
		"code": "`"}[n.Data]
	if n.Data == "a" {
		href := attr(n, "href")
		label := strings.TrimSpace(inner)
		if href == "" || label == "" || href == text(n) || href == "mailto:"+text(n) {
			return inner // a bare link: linked again when sent
		}
		return "[" + label + "](" + href + ")"
	}
	trimmed := strings.TrimSpace(inner)
	if marker == "" || trimmed == "" {
		return inner
	}
	// "<b>Maya </b>" is "**Maya** ": Markdown's markers hug the text.
	lead, trail := "", ""
	if strings.HasPrefix(inner, " ") {
		lead = " "
	}
	if strings.HasSuffix(inner, " ") {
		trail = " "
	}
	return lead + marker + trimmed + marker + trail
}

// write adds s (lines split at "\n") to the output. Text that would read as
// Markdown block syntax at the start of a line ("- not a list") is escaped
// when escapeStart.
func (w *mdWriter) write(s string, escapeStart bool) {
	for i, part := range strings.Split(s, "\n") {
		if i > 0 {
			w.newline()
		}
		if !w.started {
			if escapeStart { // text: its leading space means nothing here
				part = strings.TrimLeft(part, " ")
			}
			if part == "" {
				continue
			}
			w.cur.WriteString(w.prefix())
			if escapeStart && blockStart.MatchString(part) {
				part = `\` + part
			}
			w.started = true
		}
		w.cur.WriteString(part)
	}
}

var blockStart = regexp.MustCompile(`^(#{1,6}(\s|$)|>|[-+]\s|\d+[.)]\s)`)

func (w *mdWriter) prefix() string { return strings.Repeat("> ", w.quote) }

// newline ends the line; an empty one in a quote is ">".
func (w *mdWriter) newline() {
	line := w.cur.String()
	if !w.started {
		line = w.prefix()
	}
	w.lines = append(w.lines, strings.TrimRight(line, " "))
	w.cur.Reset()
	w.started = false
}

func (w *mdWriter) lineBreak() {
	if w.started {
		w.newline()
	}
}

func (w *mdWriter) lineBreakIf(cond bool) {
	if cond {
		w.lineBreak()
	}
}

// blank puts a blank line before what comes next.
func (w *mdWriter) blank() {
	w.lineBreak()
	if len(w.lines) > 0 && !isBlank(w.lines[len(w.lines)-1]) {
		w.newline()
	}
}

func isBlank(line string) bool { return strings.Trim(line, "> ") == "" }

// finish joins the lines, without blank ones at either end or two in a row.
func (w *mdWriter) finish() string {
	w.lineBreak()
	var out []string
	for _, l := range w.lines {
		if isBlank(l) && (len(out) == 0 || isBlank(out[len(out)-1])) {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && isBlank(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

var spaces = regexp.MustCompile(`[ \t\r\n\f\x{00a0}]+`)

func collapse(s string) string { return spaces.ReplaceAllString(s, " ") }

// escape backslashes what Markdown would read as formatting. An underscore
// inside a word (file_name) is left alone: Markdown doesn't read it.
func escape(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i, c := range r {
		switch {
		case strings.ContainsRune("\\*`[]~", c):
			b.WriteRune('\\')
		case c == '_' && !(i > 0 && i < len(r)-1 && isWordRune(r[i-1]) && isWordRune(r[i+1])):
			b.WriteRune('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text is all the text under n, as is.
func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
	}
	return b.String()
}
