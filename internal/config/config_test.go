package config_test

import (
	"os"
	"path/filepath"
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
	if c != config.Default() {
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
