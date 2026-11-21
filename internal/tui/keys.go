package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

func bind(help, desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
}

// keys is the single source of truth for bindings: Update matches on them
// and the help screen lists them.
var keys = struct {
	Up, Down, PageUp, PageDown, HalfUp, HalfDown, Top, Bottom, Left, Right, Open, Back, Next, Prev,
	Archive, Trash, Inbox, Read, Star, Select, Undo, Expand, Browser,
	Search, Sync, PrevAccount, NextAccount, Sidebar, GoTo, Help, Quit key.Binding
}{
	Up:          bind("k/↑", "up", "k", "up"),
	Down:        bind("j/↓", "down", "j", "down"),
	PageUp:      bind("K/pgup", "page up", "K", "pgup"),
	PageDown:    bind("J/pgdn/space", "page down", "J", "pgdown", " "),
	HalfUp:      bind("ctrl-u", "half a page up", "ctrl+u"),
	HalfDown:    bind("ctrl-d", "half a page down", "ctrl+d"),
	Top:         bind("gg", "top", "g"),
	Bottom:      bind("G", "bottom", "G"),
	Left:        bind("h/←", "pane to the left", "h", "left"),
	Right:       bind("l/→", "pane to the right", "l", "right"),
	Open:        bind("enter", "open", "enter"),
	Back:        bind("esc", "back; clear selection or search", "esc"),
	Next:        bind("n", "next conversation (reader)", "n"),
	Prev:        bind("p", "previous conversation (reader)", "p"),
	Archive:     bind("a", "archive", "a"),
	Trash:       bind("dd", "move to Trash", "d"),
	Inbox:       bind("I", "move to Inbox", "I"),
	Read:        bind("N", "mark read / unread", "N"),
	Star:        bind("*", "star / unstar", "*"),
	Select:      bind("x", "select (then act on all selected)", "x"),
	Undo:        bind("u", "undo the last change", "u"),
	Expand:      bind("o", "expand / collapse messages (reader)", "o"),
	Browser:     bind("O", "open in Gmail on the web", "O"),
	Search:      bind("/", "search all mail", "/"),
	Sync:        bind("s", "sync now", "s"),
	PrevAccount: bind("[", "previous account", "["),
	NextAccount: bind("]", "next account", "]"),
	Sidebar:     bind("b", "show / hide mailboxes (wide screens)", "b"),
	GoTo:        bind("gi gs gt…", "go to Inbox, Starred, Sent, Drafts, All mail, Trash", "g"),
	Help:        bind("?", "help", "?"),
	Quit:        bind("q", "quit", "q", "ctrl+c"),
}

// goTo maps the key after g to a mailbox, like Gmail's shortcuts.
var goTo = map[string]string{"i": "INBOX", "s": "STARRED", "t": "SENT", "d": "DRAFT", "a": "", "#": "TRASH", "!": "SPAM"}

func helpText() string {
	groups := []struct {
		name  string
		binds []key.Binding
	}{
		{"Move", []key.Binding{keys.Down, keys.Up, keys.PageDown, keys.PageUp, keys.HalfDown, keys.HalfUp,
			keys.Top, keys.Bottom, keys.Left, keys.Right, keys.Open, keys.Back, keys.Next, keys.Prev}},
		{"Act on the conversation (or the selected ones)", []key.Binding{keys.Archive, keys.Trash, keys.Inbox,
			keys.Read, keys.Star, keys.Select, keys.Undo, keys.Expand, keys.Browser}},
		{"Everywhere", []key.Binding{keys.Search, keys.GoTo, keys.Sync, keys.PrevAccount, keys.NextAccount,
			keys.Sidebar, keys.Help, keys.Quit}},
	}
	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(title.Render(g.name) + "\n")
		for _, k := range g.binds {
			b.WriteString("  " + accentFg.Render(fit(k.Help().Key, 13)) + k.Help().Desc + "\n")
		}
	}
	b.WriteString("\n" + dim.Render("gi Inbox · gs Starred · gt Sent · gd Drafts · ga All mail · g# Trash · g! Spam"))
	b.WriteString("\n" + dim.Render("Press any key to close."))
	return b.String()
}
