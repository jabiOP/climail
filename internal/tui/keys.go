package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

func bind(help, desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
}

// keys is the single source of truth for bindings: Update matches on them
// and the help screen lists them.
var keys = struct {
	Up, Down, PageUp, PageDown, HalfUp, HalfDown, Top, Bottom, Left, Right, Open, Back, Next, Prev, NextMessage, PrevMessage, ReadNext, ReadPrev,
	Archive, Trash, Inbox, Read, Star, Select, Together, Undo, Expand, Browser, Compose, Reply, ReplyAll, Forward, SaveFiles, OpenFile, Links,
	Search, Sync, PrevAccount, NextAccount, Sidebar, PickColor, Log, Help, Quit, ForceQuit key.Binding
}{
	Up:          bind("k", "up", "k", "up"),
	Down:        bind("j", "down", "j", "down"),
	PageUp:      bind("K", "page up", "K", "pgup"),
	PageDown:    bind("J", "page down", "J", "pgdown", " "),
	HalfUp:      bind("^u", "half a page up", "ctrl+u"),
	HalfDown:    bind("^d", "half a page down", "ctrl+d"),
	Top:         bind("gg", "top", "g"),
	Bottom:      bind("G", "bottom", "G"),
	Left:        bind("h", "pane to the left", "h", "left"),
	Right:       bind("l", "pane to the right", "l", "right"),
	Open:        bind("enter", "open", "enter"),
	Back:        bind("esc", "back; clear selection or search", "esc"),
	Next:        bind("n", "next conversation (reader)", "n"),
	Prev:        bind("p", "previous conversation (reader)", "p"),
	NextMessage: bind("tab", "next message (reader)", "tab"),
	PrevMessage: bind("⇧tab", "previous message (reader)", "shift+tab"),
	ReadNext:    bind("J", "read the next message (reader)", "J"),
	ReadPrev:    bind("K", "read the previous message (reader)", "K"),
	Archive:     bind("a", "archive", "a"),
	Trash:       bind("dd", "move to Trash", "d"),
	Inbox:       bind("I", "move to Inbox", "I"),
	Read:        bind("N", "mark read / unread", "N"),
	Star:        bind("*", "star / unstar", "*"),
	Select:      bind("x", "select (then act on all selected)", "x"),
	Together:    bind("T", "read the selected, else the unread, together", "T"),
	Undo:        bind("u", "undo the last change (again: the ones before)", "u"),
	Expand:      bind("o", "expand / collapse messages (reader)", "o"),
	Browser:     bind("O", "open in Gmail on the web", "O"),
	Compose:     bind("c", "write a new mail", "c"),
	Reply:       bind("R", "reply", "R"),
	ReplyAll:    bind("r", "reply all", "r"),
	Forward:     bind("f", "forward", "f"),
	SaveFiles:   bind("D", "save the attachments", "D"),
	OpenFile:    bind("A", "open an attachment", "A"),
	Links:       bind("U", "open a link in the message", "U"),
	Search:      bind("/", "search all mail", "/"),
	Sync:        bind("s", "sync now", "s"),
	PrevAccount: bind("[", "previous account", "["),
	NextAccount: bind("]", "next account", "]"),
	Sidebar:     bind("b", "show / hide mailboxes (wide screens)", "b"),
	PickColor:   bind("C", "the account's color (mailboxes)", "C"),
	Log:         bind("L", "what Gmail rejected", "L"),
	Help:        bind("?", "help", "?"),
	Quit:        bind("ZZ", "quit", "Z"),
	ForceQuit:   bind("^c", "quit", "ctrl+c"),
}

// goTo lists the mailboxes the key after g opens, like Gmail's shortcuts.
var goTo = []struct{ key, box, name string }{
	{"i", "INBOX", "Inbox"}, {"s", "STARRED", "Starred"}, {"t", "SENT", "Sent"}, {"d", "DRAFT", "Drafts"},
	{"a", "", "All Mail"}, {"#", "TRASH", "Trash"}, {"!", "SPAM", "Spam"},
}

type helpRow struct{ keys, desc string }

func one(b key.Binding, desc string) helpRow { return helpRow{b.Help().Key, desc} }

func pair(a, b key.Binding, desc string) helpRow {
	return helpRow{a.Help().Key + "/" + b.Help().Key, desc}
}

// helpText is the ? screen: two columns, so it fits an 80×24 terminal.
func helpText() string {
	var gotoRows []helpRow // two to a row: "gi/gs  Inbox / Starred"
	for i := 0; i < len(goTo); i += 2 {
		r := helpRow{"g" + goTo[i].key, goTo[i].name}
		if i+1 < len(goTo) {
			r.keys += "/g" + goTo[i+1].key
			r.desc += " / " + goTo[i+1].name
		}
		gotoRows = append(gotoRows, r)
	}
	left := helpColumn(
		"Move", []helpRow{
			pair(keys.Down, keys.Up, "down / up"), pair(keys.PageDown, keys.PageUp, "page; sidebar: account"),
			pair(keys.HalfDown, keys.HalfUp, "half page down / up"), pair(keys.Top, keys.Bottom, "top / bottom"),
			pair(keys.Left, keys.Right, "pane left / right"), pair(keys.Open, keys.Back, "open / back"),
			pair(keys.Next, keys.Prev, "next / prev (reader)"),
			pair(keys.NextMessage, keys.PrevMessage, "next / prev message"),
		},
		"Everywhere", []helpRow{
			one(keys.Compose, "write a new mail"), one(keys.Search, "search all mail"), one(keys.Sync, "sync now"),
			pair(keys.PrevAccount, keys.NextAccount, "previous / next account"),
			one(keys.Sidebar, "show / hide mailboxes"), one(keys.PickColor, "account color (sidebar)"),
			one(keys.Log, "what Gmail rejected"),
			one(keys.Help, "this help"), one(keys.Quit, "quit (q: in the list)"),
		})
	right := helpColumn(
		"Conversations", []helpRow{
			pair(keys.ReplyAll, keys.Reply, "reply all / reply"), one(keys.Forward, "forward"),
			pair(keys.OpenFile, keys.SaveFiles, "open / save attachments"), one(keys.Links, "open a link"),
			one(keys.Archive, "archive"), one(keys.Trash, "move to Trash"), one(keys.Inbox, "move to Inbox"),
			one(keys.Read, "mark read / unread"), one(keys.Star, "star / unstar"),
			pair(keys.Select, keys.Together, "select / read together"), one(keys.Undo, "undo (again: earlier)"),
			one(keys.Expand, "expand / collapse all"), one(keys.Browser, "open here in Gmail (web)"),
		},
		"Go to", gotoRows)
	cols := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
	return cols + "\n\n" + dim.Render("Press any key to close.")
}

// helpColumn renders titled groups of rows (pairs of title and rows).
func helpColumn(groups ...any) string {
	var lines []string
	for i := 0; i < len(groups); i += 2 {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, title.Render(groups[i].(string)))
		for _, r := range groups[i+1].([]helpRow) {
			lines = append(lines, accentFg.Render(fit(r.keys, 10))+fit(r.desc, 24))
		}
	}
	return strings.Join(lines, "\n")
}
