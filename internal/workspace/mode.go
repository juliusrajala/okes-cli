// Package workspace defines local-directory workspace preparation and ownership
// rules. It does not acquire Git repositories or control container lifecycles.
package workspace

import "fmt"

// Mode selects how a supplied local directory becomes a session workspace.
// The zero value is invalid. Configuration must apply its default explicitly.
type Mode string

const (
	// Mount uses the supplied directory directly without taking ownership of it.
	Mount Mode = "mount"
	// Copy requests a session-owned filesystem copy, not a Git clone.
	Copy Mode = "copy"
)

// Validate checks the supported workspace modes. It does not inspect a source
// directory, create a copy, or establish ownership.
func (m Mode) Validate() error {
	switch m {
	case Mount, Copy:
		return nil
	case "clone":
		return fmt.Errorf("workspace mode %q is no longer supported: Git provisioning is outside go-sesh; choose mount or filesystem copy", m)
	default:
		return fmt.Errorf("invalid workspace mode %q: expected mount or copy", m)
	}
}
