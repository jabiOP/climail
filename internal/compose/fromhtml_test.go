package compose_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
)

func TestMarkdownFromHTML_GmailsComposeHTML(t *testing.T) {
	src := `<div dir="ltr">Hi <b>Maya</b>, <i>quick</i> one about <a href="https://x.example/plan">the plan</a>:` +
		`<div><br></div><div>Second *line* with a_b and <s>old</s> https://x.example</div>` +
		`<ul><li>one</li><li>two<ul><li>deep</li></ul></li></ul><ol><li>first</li></ol>` +
		`<div>- not a list</div></div><br><div class="gmail_quote"><div dir="ltr" class="gmail_attr">On Thu, Tom &lt;<a href="mailto:tom@x.example">tom@x.example</a>&gt; wrote:<br></div>` +
		`<blockquote class="gmail_quote" style="margin:0px 0px 0px 0.8ex;border-left:1px solid rgb(204,204,204);padding-left:1ex"><div dir="ltr">Old text<br>more</div></blockquote></div>`

	got, lossy := compose.MarkdownFromHTML(src)

	want := "Hi **Maya**, *quick* one about [the plan](https://x.example/plan):\n\n" +
		"Second \\*line\\* with a_b and ~~old~~ https://x.example\n\n" +
		"- one\n- two\n  - deep\n\n1. first\n\n\\- not a list\n\n" +
		"On Thu, Tom <tom@x.example> wrote:\n> Old text\n> more\n"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if lossy {
		t.Error("lossy: Markdown holds all of this")
	}
}

func TestMarkdownFromHTML_FormattingMarkdownCantHold(t *testing.T) {
	for _, src := range []string{
		`<div><font face="monospace">code-ish</font></div>`,
		`<div><span style="color:rgb(255,0,0)">red</span></div>`,
		`<div><img src="cid:ii_1" alt="photo"></div>`,
		`<div><u>underlined</u></div>`,
		`<div style="text-align:center">centred</div>`,
	} {
		if _, lossy := compose.MarkdownFromHTML(src); !lossy {
			t.Errorf("%s: not lossy", src)
		}
	}
	if got, _ := compose.MarkdownFromHTML(`<div><font color="#ff0000">red</font> text</div>`); got != "red text\n" {
		t.Errorf("text lost: %q", got)
	}
}

func TestMarkdownFromHTML_RoundTripsWhatClimailSends(t *testing.T) {
	md := "Hi **Maya**,\nsee [the plan](https://x.example/plan).\n\n- one\n- two\n\n> quoted\n> lines\n"
	html, err := compose.HTML(md)
	if err != nil {
		t.Fatal(err)
	}
	if got, lossy := compose.MarkdownFromHTML(html); got != md || lossy {
		t.Errorf("got %q (lossy %v), want %q", got, lossy, md)
	}
}

func TestDraftBody(t *testing.T) {
	typed := "Hi **Maya**,\nsnake_case and *stars*\n"
	ours, _ := compose.HTML(typed)
	cases := []struct {
		name      string
		m         model.Message
		want      string
		wantLossy bool
	}{
		{"plain text", model.Message{Body: "just text\n"}, "just text\n", false},
		{"saved by climail: as typed", model.Message{Body: typed, HTML: ours}, typed, false},
		{"from the web", model.Message{Body: "Hi Maya", HTML: `<div dir="ltr">Hi <b>Maya</b></div>`}, "Hi **Maya**\n", false},
		{"web, with a font", model.Message{Body: "Hi", HTML: `<div><font face="serif">Hi</font></div>`}, "Hi\n", true},
	}
	for _, c := range cases {
		got, lossy := compose.DraftBody(c.m)
		if got != c.want || lossy != c.wantLossy {
			t.Errorf("%s: %q, lossy %v; want %q, %v", c.name, got, lossy, c.want, c.wantLossy)
		}
	}
}
