package gmail

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
)

// Errors the sync engine reacts to. Anything else is treated as transient
// (network trouble, 5xx, rate limits) and retried later.
var (
	ErrNotFound       = errors.New("not found on Google")
	ErrHistoryExpired = errors.New("sync history expired") // 404 on history.list: do a full sync
	ErrPermanent      = errors.New("rejected by Google")   // other 4xx: retrying won't help
	// ErrUnauthorized (401) is not permanent: the token may refresh, or the
	// user may log in again. Changes stay queued.
	ErrUnauthorized = errors.New("Google rejected our login (run `climail auth login` if this persists)")
)

func mapErr(err error) error {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return err
	}
	switch code := gerr.Code; {
	case code == http.StatusNotFound:
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case code == http.StatusUnauthorized:
		return fmt.Errorf("%w: %v", ErrUnauthorized, err)
	case code == http.StatusTooManyRequests || isRateLimit(gerr):
		return err
	case code >= 400 && code < 500:
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	return err
}

// Google reports rate and quota limits as 403 with reasons like
// "rateLimitExceeded", "userRateLimitExceeded", "dailyLimitExceeded", "quotaExceeded".
func isRateLimit(gerr *googleapi.Error) bool {
	for _, item := range gerr.Errors {
		reason := strings.ToLower(item.Reason)
		if strings.Contains(reason, "limitexceeded") || strings.Contains(reason, "quotaexceeded") {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
