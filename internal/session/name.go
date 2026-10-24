package session

import (
	"fmt"
	"regexp"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ValidateName checks the human-readable alias format used by pi-sesh.
// Names are case-sensitive and can be reused only after their reservation is released.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid session name %q: use 1-64 ASCII letters, numbers, '.', '_' or '-', starting with a letter or number", name)
	}
	return nil
}
