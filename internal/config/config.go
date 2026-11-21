package config

import (
	"errors"
	"fmt"
	"io/fs"
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
}

func Default() Config {
	return Config{
		SyncInterval: Duration{30 * time.Second},
		StaleAfter:   Duration{2 * time.Minute},
		SyncTimeout:  Duration{10 * time.Second},
		SyncThreads:  300,
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
	return nil
}
