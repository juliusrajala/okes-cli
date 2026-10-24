package runtime

import "fmt"

// NetworkMode selects a session's requested network policy. The zero value is
// invalid. Configuration must explicitly default an omitted setting to NetworkNone.
type NetworkMode string

const (
	// NetworkNone requests a container with networking disabled.
	NetworkNone NetworkMode = "none"
	// NetworkPasta requests rootless networking. The runtime adapter must check
	// that the local engine supports it before creating a container.
	NetworkPasta NetworkMode = "pasta"
)

// Validate checks the supported network modes. It does not enable networking
// or verify that the local engine supports the requested mode.
func (m NetworkMode) Validate() error {
	if networkMode := m; networkMode != NetworkNone && networkMode != NetworkPasta {
		return fmt.Errorf("invalid network mode: %q", networkMode)
	}

	return nil
}
