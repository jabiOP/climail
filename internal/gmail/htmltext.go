package gmail

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// HTMLToText renders an HTML mail body as readable plain text: paragraphs and
// line breaks kept, scripts and styles dropped, list items bulleted, and links
// numbered like footnotes ("Read more[1]" … "[1] https://…"), so long tracking
// URLs don't break up the text.
func HTMLToText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	var links []string
	var href string // the link being read, until its </a>
	skip := 0       // inside <script>, <style>, <head>…
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return finish(b.String(), links)
		case html.TextToken:
			if skip == 0 {
				b.WriteString(collapse(string(z.Text())))
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			for more := true; more && tt != html.EndTagToken; {
				var k, v []byte
				k, v, more = z.TagAttr()
				attrs[string(k)] = string(v)
			}
			switch tag {
			case "script", "style", "head", "title":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if skip > 0 {
				continue
			}
			switch {
			case tag == "br":
				b.WriteString("\n")
			case tag == "li" && tt != html.EndTagToken:
				b.WriteString("\n• ")
			case tag == "a" && tt == html.StartTagToken:
				href = attrs["href"]
			case tag == "a" && tt == html.EndTagToken:
				if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
					links = append(links, href)
					fmt.Fprintf(&b, "[%d]", len(links))
				}
				href = ""
			case tag == "img" && attrs["alt"] != "":
				b.WriteString(attrs["alt"])
			case tag == "td" || tag == "th":
				b.WriteString(" ")
			case blockTags[tag]:
				b.WriteString("\n\n")
			}
		}
	}
}

var blockTags = map[string]bool{
	"p": true, "div": true, "tr": true, "table": true, "ul": true, "ol": true, "blockquote": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "hr": true, "section": true,
	"article": true, "header": true, "footer": true, "pre": true,
}

var spaces = regexp.MustCompile(`[ \t\r\n\f\x{00a0}]+`)

// collapse squeezes HTML whitespace runs to one space, as a browser does.
func collapse(s string) string { return spaces.ReplaceAllString(s, " ") }

var blankLines = regexp.MustCompile(`\n{3,}`)

func finish(text string, links []string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	text = strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
	if len(links) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text + "\n\n")
	for i, l := range links {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, l)
	}
	return strings.TrimRight(b.String(), "\n")
}
