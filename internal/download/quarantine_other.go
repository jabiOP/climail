//go:build !darwin

package download

// quarantine does nothing outside macOS: attachments are saved without the
// executable bit, so they can't run as programs when opened.
func quarantine(string) error { return nil }
