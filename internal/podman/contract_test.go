package podman_test

import (
	"go-sesh/internal/podman"
	"go-sesh/internal/session"
)

// Go checks interface compatibility by method signatures, without an implements
// declaration. Keep the adapter independent of the session package in production.
var _ session.Inspector = (*podman.Client)(nil)
