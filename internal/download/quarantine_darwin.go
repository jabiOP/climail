package download

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// quarantine marks a saved file as downloaded, as Mail and browsers do, so
// macOS checks it and asks before opening it. A volume that can't hold the
// mark is left as it is.
func quarantine(path string) error {
	value := fmt.Sprintf("0081;%08x;climail;", time.Now().Unix())
	err := unix.Setxattr(path, "com.apple.quarantine", []byte(value), 0)
	if errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	return err
}
