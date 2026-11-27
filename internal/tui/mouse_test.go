package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func newMouseFixture(t *testing.T, w, h int) *fixture {
	t.Helper()
	return newFixtureWith(t, w, h, Deps{Mouse: true})
}

// at is where text first shows on screen, after the frame column at
// least, so a pane's title in the border isn't taken for its rows.
func (f *fixture) at(text string, fromX int) (x, y int) {
	f.t.Helper()
	for y, l := range strings.Split(f.view(), "\n") {
		cells := []rune(l)
		for x := fromX; x < len(cells); x++ {
			if strings.HasPrefix(string(cells[x:]), text) {
				return lipgloss.Width(string(cells[:x])), y
			}
		}
	}
	f.t.Fatalf("%q not on screen:\n%s", text, f.view())
	return 0, 0
}

func (f *fixture) click(x, y int) {
	f.t.Helper()
	f.send(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func (f *fixture) clickOn(text string, fromX int) {
	f.t.Helper()
	f.click(f.at(text, fromX))
}

func (f *fixture) wheel(x, y int, b tea.MouseButton) {
	f.t.Helper()
	f.send(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
}

func TestMouse_OffByDefault(t *testing.T) {
	f := newFixture(t, 150, 22)

	f.clickOn("Priya Raman", 30)

	if f.model().cursor != 0 {
		t.Errorf("cursor = %d, want 0: the mouse is off unless configured", f.model().cursor)
	}
}

func TestMouse_ClickSelectsAConversationThenOpensIt(t *testing.T) {
	f := newMouseFixture(t, 150, 22)

	f.clickOn("Priya Raman", 30)
	if m := f.model(); m.cursor != 4 || m.focus != paneList {
		t.Fatalf("cursor %d, focus %v: want 4 (Priya's), the list", m.cursor, m.focus)
	}

	f.clickOn("Priya Raman", 30)

	if f.model().focus != paneReader {
		t.Errorf("focus = %v, want the reader: a click on the selected conversation opens it", f.model().focus)
	}
	if t0, _ := f.model().current(); t0.Unread() {
		t.Error("opened by a click, the conversation is still unread")
	}
}

func TestMouse_ClickOnAMailboxOpensIt(t *testing.T) {
	f := newMouseFixture(t, 150, 22)

	f.clickOn("Starred", 1)

	if f.model().box.Name != "Starred" {
		t.Errorf("mailbox = %q, want Starred", f.model().box.Name)
	}
}

func TestMouse_ClickOnAnotherAccountShowsIt(t *testing.T) {
	f := newMouseFixture(t, 150, 22)

	f.clickOn("you@home.example", 1)

	if f.model().account != homeAcct {
		t.Errorf("account = %q, want %q", f.model().account, homeAcct)
	}
}

func TestMouse_ClickOnTheReaderOpensTheConversation(t *testing.T) {
	f := newMouseFixture(t, 150, 22)

	f.clickOn("Agreed, added", 90)

	if f.model().focus != paneReader {
		t.Errorf("focus = %v, want the reader", f.model().focus)
	}
}

func TestMouse_ClickOnAMessageOpensIt(t *testing.T) {
	f := newMouseFixture(t, 150, 22)
	f.keys("enter")

	f.clickOn("Tom Okafor ", 90) // collapsed: one line

	p, _ := f.model().pointed()
	if p.From.Name != "Tom Okafor" || !f.model().expanded[p.ID] {
		t.Errorf("pointed = %s (open %v), want Tom's message, opened", p.From.Name, f.model().expanded[p.ID])
	}
	if !strings.Contains(f.view(), "▶ Tom Okafor") {
		t.Errorf("▶ isn't on Tom's message:\n%s", f.view())
	}
}

func TestMouse_WheelMovesTheListAndScrollsTheReader(t *testing.T) {
	f := newMouseFixture(t, 150, 22)

	f.wheel(60, 5, tea.MouseButtonWheelDown)
	f.wheel(60, 5, tea.MouseButtonWheelDown)
	if f.model().cursor != 2 {
		t.Errorf("cursor = %d, want 2", f.model().cursor)
	}
	f.wheel(60, 5, tea.MouseButtonWheelUp)
	if f.model().cursor != 1 {
		t.Errorf("cursor = %d, want 1", f.model().cursor)
	}

	f = newMouseFixture(t, 150, 10) // the conversation is longer than the reader
	f.keys("enter")
	f.wheel(120, 5, tea.MouseButtonWheelDown)
	if f.model().scroll != 1 {
		t.Errorf("reader scroll = %d, want 1", f.model().scroll)
	}
}

func TestMouse_NarrowClicksTheOnePaneShown(t *testing.T) {
	f := newMouseFixture(t, 80, 16)

	f.clickOn("Priya Raman", 0)
	f.clickOn("Priya Raman", 0)

	if f.model().focus != paneReader {
		t.Errorf("focus = %v, want the reader", f.model().focus)
	}
}

func TestMouse_ClickClosesHelp(t *testing.T) {
	f := newMouseFixture(t, 150, 22)
	f.keys("?")

	f.click(5, 5)

	if f.model().help {
		t.Error("help still shown after a click")
	}
}

func TestMouse_ClicksOnTheFrameAndStatusBarDoNothing(t *testing.T) {
	f := newMouseFixture(t, 150, 22)

	f.click(60, 0)  // the list's title
	f.click(60, 20) // its bottom edge
	f.click(60, 21) // the status bar

	if m := f.model(); m.cursor != 0 || m.focus != paneList {
		t.Errorf("cursor %d, focus %v: want 0, the list", m.cursor, m.focus)
	}
}
