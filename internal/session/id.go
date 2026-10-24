package session

import (
	"fmt"
	"regexp"
)

// ID identifies one session. A human-readable name can be reused; an ID must not
// be reused. The zero value is invalid. Validate IDs again at input boundaries,
// because an explicit conversion from string can bypass ParseID.
type ID string

var idPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ParseID validates a UUID-shaped session identifier. It preserves the input's
// letter case so that parsing does not silently change an existing identity.
func ParseID(value string) (ID, error) {
	id := ID(value)
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

// Validate checks the UUID format accepted by the TypeScript implementation.
// A valid format does not establish that a session exists or belongs to us.
func (id ID) Validate() error {
	if !idPattern.MatchString(string(id)) {
		return fmt.Errorf("invalid session ID %q: expected a UUID with version 1-8 and RFC variant bits", id)
	}
	return nil
}
