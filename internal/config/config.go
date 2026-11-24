package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration lets TOML values like "45s" decode into a time.Duration.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type Config struct {
	SyncInterval Duration `toml:"sync_interval"` // TUI poll period
	StaleAfter   Duration `toml:"stale_after"`   // CLI reads sync first if older than this
	SyncTimeout  Duration `toml:"sync_timeout"`  // max time a CLI command waits for that sync
	SyncThreads  int      `toml:"sync_threads"`  // conversations a full sync fetches per account, newest first
	DownloadDir  string   `toml:"download_dir"`  // where attachments are saved
	// PreviewAtLaunch shows the first conversation in the reader when the TUI opens.
	PreviewAtLaunch bool `toml:"preview_at_launch"`
	SidebarAtLaunch bool `toml:"sidebar_at_launch"` // the mailboxes are shown when the TUI opens
	// AccountColors gives accounts a color, by address: a Colors name or "#rrggbb".
	AccountColors map[string]string `toml:"account_colors"`
}

func Default() Config {
	return Config{
		SyncInterval:    Duration{30 * time.Second},
		StaleAfter:      Duration{2 * time.Minute},
		SyncTimeout:     Duration{10 * time.Second},
		SyncThreads:     300,
		DownloadDir:     "~/Downloads",
		SidebarAtLaunch: true,
	}
}

// Load reads path over the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	md, err := toml.DecodeFile(path, &c)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		var raw struct {
			AccountColors map[string]any `toml:"account_colors"`
		}
		if _, rerr := toml.DecodeFile(path, &raw); rerr == nil {
			for key := range raw.AccountColors {
				if !strings.Contains(key, "@") {
					return Config{}, fmt.Errorf("%s: %w", path, misplaced(key))
				}
			}
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %q", path, und[0].String())
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c Config) validate() error {
	if c.SyncInterval.Duration < 10*time.Second {
		return fmt.Errorf("sync_interval must be at least 10s (Google rate limits), got %v", c.SyncInterval)
	}
	if c.SyncThreads < 1 {
		return fmt.Errorf("sync_threads must be at least 1, got %d", c.SyncThreads)
	}
	for account, color := range c.AccountColors {
		if !strings.Contains(account, "@") {
			return misplaced(account)
		}
		if ColorHex(color) == "" {
			return fmt.Errorf("account_colors: %s: %q is neither #rrggbb nor one of %s", account, color, colorNames())
		}
	}
	return nil
}

// misplaced is the error for a setting written below [account_colors]: TOML
// puts every key after a table's header in that table.
func misplaced(key string) error {
	return fmt.Errorf("%s must go above [account_colors] (keys below it are account colors)", key)
}

// Color is one of Google Calendar's colors.
type Color struct {
	Name, Hex string
	Row       int // its family, as clindar's color grid groups them: reds, oranges… greys
}

// Colors are Google Calendar's 24 colors, the extended palette clindar
// offers, in the same shades: by family, light to dark.
var Colors = []Color{
	{"Flamingo", "#e67c73", 0}, {"Tomato", "#d50000", 0},
	{"Tangerine", "#f4511e", 1}, {"Mango", "#f09300", 1}, {"Pumpkin", "#ef6c00", 1},
	{"Citron", "#e4c441", 2}, {"Banana", "#f6bf26", 2}, {"Avocado", "#c0ca33", 2},
	{"Pistachio", "#7cb342", 3}, {"Sage", "#33b679", 3}, {"Basil", "#0b8043", 3},
	{"Eucalyptus", "#009688", 4},
	{"Lavender", "#7986cb", 5}, {"Cobalt", "#4285f4", 5}, {"Blueberry", "#3f51b5", 5}, {"Peacock", "#039be5", 5},
	{"Wisteria", "#b39ddb", 6}, {"Amethyst", "#9e69af", 6}, {"Grape", "#8e24aa", 6},
	{"Cherry blossom", "#d81b60", 7}, {"Radicchio", "#ad1457", 7},
	{"Cocoa", "#795548", 8},
	{"Birch", "#a79b8e", 9}, {"Graphite", "#616161", 9},
}

// AutoColors are given to accounts without one, in the order they were
// added: Google Calendar's classic colors, the most distinct first.
var AutoColors = []string{"Peacock", "Tomato", "Basil", "Banana", "Grape", "Tangerine", "Blueberry", "Flamingo",
	"Sage", "Lavender", "Graphite"}

func colorNames() string {
	names := make([]string, len(Colors))
	for i, c := range Colors {
		names[i] = strings.ToLower(c.Name)
	}
	return strings.Join(names, ", ")
}

// ColorHex is a color's "#rrggbb", given that or a Colors name (any case); "" if it is neither.
func ColorHex(color string) string {
	for _, c := range Colors {
		if strings.EqualFold(color, c.Name) {
			return c.Hex
		}
	}
	var r, g, b int
	if n, err := fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b); err == nil && n == 3 && len(color) == 7 {
		return strings.ToLower(color)
	}
	return ""
}

// SetAccountColor writes an account's color into the config file's
// [account_colors] table, leaving the rest of the file, comments included, as it is.
func SetAccountColor(path, account, color string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entry := strconv.Quote(account) + " = " + strconv.Quote(color)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(data) == 0 {
		lines = nil
	}
	table, end := -1, len(lines) // the table's header line, and the line after its last entry
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "[") {
			if table >= 0 && end == len(lines) && strings.HasPrefix(t, strconv.Quote(account)) {
				lines[i] = entry
				return writeLines(path, lines)
			}
			continue
		}
		if table >= 0 && end == len(lines) {
			end = i
		}
		if t == "[account_colors]" {
			table = i
		}
	}
	if table < 0 && len(lines) == 0 {
		return writeLines(path, []string{"[account_colors]", entry})
	}
	if table < 0 {
		return writeLines(path, append(lines, "", "[account_colors]", entry))
	}
	for end > table+1 && strings.TrimSpace(lines[end-1]) == "" { // before the blank lines ending the table
		end--
	}
	return writeLines(path, slices.Insert(lines, end, entry))
}

func writeLines(path string, lines []string) error {
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
