// Package cli implements the climail command line with cobra. Commands are
// thin: parse flags, call the service, print with the output package.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jabiOP/climail/internal/config"
	"github.com/jabiOP/climail/internal/service"
	"github.com/jabiOP/climail/internal/store"
	"github.com/jabiOP/climail/internal/syncer"
)

var ErrNotLoggedIn = errors.New("no account: run `climail auth login` (first time? see `climail auth setup --help`)")

// App holds everything commands need. Main builds the real one; tests build
// one with temp dirs, a fixed clock, buffers, and a fake Remote.
type App struct {
	Paths  config.Paths
	Config config.Config
	Now    func() time.Time
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	IsTTY  bool // stdout is a terminal
	// Remote builds the Gmail client for an account.
	Remote func(ctx context.Context, account string) (syncer.Remote, error)
	// OpenURL opens a link in the browser (nil: links can't be opened).
	OpenURL func(string) error

	store *store.Store
}

// Store opens the database on first use.
func (a *App) Store() (*store.Store, error) {
	if a.store != nil {
		return a.store, nil
	}
	if err := a.Paths.EnsureDirs(); err != nil {
		return nil, err
	}
	st, err := store.Open(a.Paths.DBFile())
	if err != nil {
		return nil, err
	}
	a.store = st
	return st, nil
}

func (a *App) Close() error {
	if a.store == nil {
		return nil
	}
	return a.store.Close()
}

func (a *App) Service() (*service.Service, error) {
	st, err := a.Store()
	if err != nil {
		return nil, err
	}
	return service.New(st), nil
}

// Accounts lists the logged-in accounts; ErrNotLoggedIn if there are none.
func (a *App) Accounts(ctx context.Context) ([]string, error) {
	st, err := a.Store()
	if err != nil {
		return nil, err
	}
	ids, err := st.Accounts(ctx)
	if err == nil && len(ids) == 0 {
		err = ErrNotLoggedIn
	}
	return ids, err
}

// Account picks the account a command acts on: the one named (its address or
// the start of it, "work" for work@example.com), else the first logged in.
func (a *App) Account(ctx context.Context, name string) (string, error) {
	ids, err := a.Accounts(ctx)
	if err != nil {
		return "", err
	}
	if name == "" {
		return ids[0], nil
	}
	var matches []string
	for _, id := range ids {
		if id == name {
			return id, nil
		}
		if strings.HasPrefix(id, name) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", usageError{fmt.Errorf("no account %q (accounts: %s)", name, strings.Join(ids, ", "))}
	}
	return "", usageError{fmt.Errorf("%q matches several accounts: %s", name, strings.Join(matches, ", "))}
}

func (a *App) engine(ctx context.Context, account string) (*syncer.Engine, error) {
	st, err := a.Store()
	if err != nil {
		return nil, err
	}
	remote, err := a.Remote(ctx, account)
	if err != nil {
		return nil, err
	}
	return &syncer.Engine{Store: st, Remote: remote, Account: account, Now: a.Now, MaxThreads: a.Config.SyncThreads}, nil
}

// SyncNow syncs the accounts (all of them if none are given), unless another
// process is already syncing, in which case ran is false and that's fine.
// progress, if set, hears how a first sync's download goes.
func (a *App) SyncNow(ctx context.Context, progress func(account string, done, total int), accounts ...string) (ran bool, err error) {
	if len(accounts) == 0 {
		if accounts, err = a.Accounts(ctx); err != nil {
			return false, err
		}
	}
	return syncer.WithLock(ctx, a.Paths.LockFile(), func(ctx context.Context) error {
		var errs []error
		for _, account := range accounts {
			e, err := a.engine(ctx, account)
			if err == nil {
				if progress != nil {
					e.Progress = func(done, total int) { progress(account, done, total) }
				}
				err = e.Sync(ctx)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", account, err))
			}
		}
		return errors.Join(errs...)
	})
}

// RefreshIfStale is called by read commands: sync first if the account's
// last sync is older than stale_after, unless offline. A failed sync only
// warns: reads work from the local cache.
func (a *App) RefreshIfStale(ctx context.Context, account string, offline bool) {
	if offline {
		return
	}
	if last, err := a.store.LastSync(ctx, account); err == nil && a.Now().Sub(last) < a.Config.StaleAfter.Duration {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, a.Config.SyncTimeout.Duration)
	defer cancel()
	if _, err := a.SyncNow(ctx, nil, account); err != nil {
		fmt.Fprintf(a.Stderr, "climail: warning: sync failed, showing local data: %v\n", err)
	}
}

// PushAfterWrite is called by write commands so changes reach Gmail promptly.
// If that fails, the warning says whether they did.
func (a *App) PushAfterWrite(ctx context.Context, account string, offline bool) {
	if offline {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, a.Config.SyncTimeout.Duration)
	defer cancel()
	ran, err := a.SyncNow(ctx, nil, account)
	for !ran && err == nil && ctx.Err() == nil { // another process is syncing: wait for it, then push
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
			ran, err = a.SyncNow(ctx, nil, account)
		}
	}
	switch {
	case !ran && err == nil:
		fmt.Fprintln(a.Stderr, "climail: warning: saved here; another sync is busy, so it goes with the next one")
	case err == nil:
	case errors.Is(err, syncer.ErrPull):
		fmt.Fprintf(a.Stderr, "climail: warning: sent to Gmail; refreshing from Gmail failed: %v\n", err)
	default:
		fmt.Fprintf(a.Stderr, "climail: warning: saved here; not sent to Gmail yet (it goes with the next sync): %v\n", err)
	}
}
