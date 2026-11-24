package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jabiOP/climail/internal/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_MissingFileGivesDefaults(t *testing.T) {
	c, err := config.Load(filepath.Join(t.TempDir(), "nope.toml"))

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, config.Default()) {
		t.Errorf("Load = %+v, want defaults", c)
	}
}

func TestLoad_OverridesDefaults(t *testing.T) {
	c, err := config.Load(writeConfig(t, "sync_interval = \"1m\"\nsync_threads = 50\n"))

	if err != nil {
		t.Fatal(err)
	}
	if c.SyncInterval.Duration != time.Minute || c.SyncThreads != 50 {
		t.Errorf("Load = %+v", c)
	}
	if c.StaleAfter != config.Default().StaleAfter {
		t.Errorf("StaleAfter = %v, want the default", c.StaleAfter)
	}
}

func TestLoad_Rejects(t *testing.T) {
	tests := []struct{ name, body, wantErr string }{
		{"unknown key", "colour = 1\n", `unknown key "colour"`},
		{"interval too short", "sync_interval = \"2s\"\n", "at least 10s"},
		{"bad sync_threads", "sync_threads = 0\n", "sync_threads"},
		{"unknown color", "[account_colors]\n\"a@b.c\" = \"mauve\"\n", `"mauve"`},
		{"setting under account_colors", "[account_colors]\n\"a@b.c\" = \"sage\"\n\npreview_at_launch = false\n",
			"preview_at_launch must go above [account_colors]"},
		{"string setting under account_colors", "[account_colors]\ndownload_dir = \"~/x\"\n",
			"download_dir must go above [account_colors]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, tt.body))

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultPaths_EnvOverrides(t *testing.T) {
	t.Setenv("CLIMAIL_CONFIG_DIR", "/c")
	t.Setenv("CLIMAIL_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")

	p, err := config.DefaultPaths()

	if err != nil {
		t.Fatal(err)
	}
	if p.ConfigDir != "/c" || p.DataDir != filepath.Join("/xdg", "climail") {
		t.Errorf("paths = %+v", p)
	}
}

func TestEnsureDirs_MakesExistingDirsPrivate(t *testing.T) {
	base := t.TempDir()
	p := config.Paths{ConfigDir: filepath.Join(base, "config"), DataDir: filepath.Join(base, "data")}
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil { // made earlier, open to others
		t.Fatal(err)
	}

	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{p.ConfigDir, p.DataDir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want 0700", d, fi.Mode().Perm(), err)
		}
	}
}

func TestLoad_AccountColors(t *testing.T) {
	c, err := config.Load(writeConfig(t, "[account_colors]\n\"a@b.c\" = \"Tomato\"\n\"d@e.f\" = \"#123abc\"\n"))

	if err != nil {
		t.Fatal(err)
	}
	if got := config.ColorHex(c.AccountColors["a@b.c"]); got != "#d50000" {
		t.Errorf("Tomato = %q, want #d50000", got)
	}
	if got := config.ColorHex(c.AccountColors["d@e.f"]); got != "#123abc" {
		t.Errorf("hex = %q", got)
	}
	if got := config.ColorHex("cherry Blossom"); got != "#d81b60" { // Google Calendar's extended palette
		t.Errorf("cherry blossom = %q, want #d81b60", got)
	}
	if n := len(config.Colors); n != 24 {
		t.Errorf("%d colors, want Google Calendar's 24", n)
	}
}

func TestLoad_AllAccountsColor(t *testing.T) {
	c, err := config.Load(writeConfig(t, "[account_colors]\n\"all\" = \"grape\"\n"))

	if err != nil {
		t.Fatal(err)
	}
	if got := config.ColorHex(c.AccountColors["all"]); got != "#8e24aa" {
		t.Errorf("all = %q, want grape", got)
	}
}

func TestSetAccountColor_KeepsTheRestOfTheFile(t *testing.T) {
	tests := []struct{ name, before, after string }{
		{"no file", "", "[account_colors]\n\"a@b.c\" = \"basil\"\n"},
		{"no table",
			"# mine\nsync_threads = 50\n",
			"# mine\nsync_threads = 50\n\n[account_colors]\n\"a@b.c\" = \"basil\"\n"},
		{"replaces the account's line",
			"[account_colors]\n\"a@b.c\" = \"tomato\"  # work\n\"d@e.f\" = \"sage\"\n",
			"[account_colors]\n\"a@b.c\" = \"basil\"\n\"d@e.f\" = \"sage\"\n"},
		{"adds to the table, before the next one",
			"[account_colors]\n\"d@e.f\" = \"sage\"\n\n[other]\nx = 1\n",
			"[account_colors]\n\"d@e.f\" = \"sage\"\n\"a@b.c\" = \"basil\"\n\n[other]\nx = 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if tt.before != "" {
				os.WriteFile(path, []byte(tt.before), 0o600)
			}

			if err := config.SetAccountColor(path, "a@b.c", "basil"); err != nil {
				t.Fatal(err)
			}

			got, _ := os.ReadFile(path)
			if string(got) != tt.after {
				t.Errorf("file =\n%s\nwant\n%s", got, tt.after)
			}
		})
	}
}

func TestLoad_LaunchOptions(t *testing.T) {
	d := config.Default()
	if d.PreviewAtLaunch || !d.SidebarAtLaunch {
		t.Errorf("defaults: preview_at_launch %v, sidebar_at_launch %v; want false, true", d.PreviewAtLaunch, d.SidebarAtLaunch)
	}

	c, err := config.Load(writeConfig(t, "preview_at_launch = true\nsidebar_at_launch = false\n"))

	if err != nil {
		t.Fatal(err)
	}
	if !c.PreviewAtLaunch || c.SidebarAtLaunch {
		t.Errorf("Load = %+v", c)
	}
}

func TestLoad_AccountAtLaunch(t *testing.T) {
	c, err := config.Load(writeConfig(t, "account_at_launch = \"all\"\n"))

	if err != nil {
		t.Fatal(err)
	}
	if c.AccountAtLaunch != "all" || config.Default().AccountAtLaunch != "" {
		t.Errorf("account_at_launch %q, default %q", c.AccountAtLaunch, config.Default().AccountAtLaunch)
	}
}
