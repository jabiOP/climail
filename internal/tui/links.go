package tui

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var bareURL = regexp.MustCompile(`https?://[^\s<>"'()\[\]{}]+`)

// linksIn are a message's web links: the footnotes HTML mail's links become,
// in their numbers' order, else the URLs written in its text, once each.
// Only http and https: a mail mustn't get the opener to run a local file.
func linksIn(body string) []string {
	var notes, bare []string
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); isFootnote(l) {
			_, u, _ := strings.Cut(l, " ")
			notes = append(notes, u)
		}
	}
	if len(notes) > 0 {
		return notes
	}
	for _, u := range bareURL.FindAllString(body, -1) {
		if u = strings.TrimRight(u, ".,;:!?"); !slices.Contains(bare, u) {
			bare = append(bare, u)
		}
	}
	return bare
}

// showLinks opens the link in the ▶ message, or lists them when there are several.
func (m Model) showLinks() (tea.Model, tea.Cmd) {
	msg, _ := m.pointed()
	links := linksIn(msg.Body)
	switch len(links) {
	case 0:
		m.message = "no links in this message"
		return m, nil
	case 1:
		return m.openLinkAt(links[0])
	}
	m.links, m.linkAt, m.linkNum = links, 0, ""
	return m, nil
}

func (m Model) openLinkAt(link string) (tea.Model, tea.Cmd) {
	m.links = nil
	if m.deps.OpenURL == nil {
		m.message = link
		return m, nil
	}
	if err := m.deps.OpenURL(link); err != nil {
		m.message, m.isError = "couldn't open "+link+": "+err.Error(), true
		return m, nil
	}
	m.message = "Opened " + host(link)
	return m, nil
}

// host is where a link goes, for messages.
func host(link string) string {
	if u, err := url.Parse(link); err == nil && u.Host != "" {
		return u.Host
	}
	return link
}

// linksKey moves through the list of links; digits go to a link by its number.
func (m Model) linksKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch {
	case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
		if n, _ := strconv.Atoi(m.linkNum + k); n >= 1 && n <= len(m.links) {
			m.linkNum, m.linkAt = m.linkNum+k, n-1
		} else if n, _ := strconv.Atoi(k); n >= 1 && n <= len(m.links) { // start again
			m.linkNum, m.linkAt = k, n-1
		}
		return m, nil
	case key.Matches(msg, keys.Down):
		m.linkAt = min(m.linkAt+1, len(m.links)-1)
	case key.Matches(msg, keys.Up):
		m.linkAt = max(m.linkAt-1, 0)
	case key.Matches(msg, keys.Open):
		return m.openLinkAt(m.links[m.linkAt])
	case key.Matches(msg, keys.Back), msg.String() == "q":
		m.links = nil
		return m, nil
	}
	m.linkNum = ""
	return m, nil
}

// linksText is the U pop-up: the ▶ message's links, numbered as in its text.
func (m Model) linksText() string {
	w := max(min(m.width-8, 110), 20)
	rows := max(m.height-8, 1)
	first := max(min(m.linkAt-rows/2, len(m.links)-rows), 0)
	lines := []string{title.Render("Links"), ""}
	numW := len(fmt.Sprint(len(m.links)))
	for i := first; i < min(first+rows, len(m.links)); i++ {
		line := fmt.Sprintf("%*d  %s", numW, i+1, fit(m.links[i], w-numW-2))
		if i == m.linkAt {
			line = lipgloss.NewStyle().Background(m.hiBg()).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(append(lines, "", dim.Render("j/k or a number, enter open · esc close")), "\n")
}
