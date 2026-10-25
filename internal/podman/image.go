package podman

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go-sesh/internal/runtime"
)

// Keep the existing project-config reference syntax. In particular, a leading
// '-' must not reach Podman as an option in place of an image argument. Matching
// this format does not prove that the image is available; that needs an engine query.
var imageReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]{0,254}$`)

var imageIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func validateImageReference(image string) error {
	if !imageReferencePattern.MatchString(image) {
		return fmt.Errorf("invalid local image reference %q", image)
	}
	return nil
}

// ImageExists checks local image availability for a future session start. It does
// not pull, build, or tag images. False with a nil error means Podman positively
// reported absence, not that execution failed. Other failures must remain errors.
func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	if err := validateImageReference(image); err != nil {
		return false, err
	}

	if _, err := c.Preflight(ctx, runtime.NetworkNone); err != nil {
		return false, err
	}

	result, err := c.invoke(ctx, "image", "exists", image)
	if err != nil {
		return false, err
	}

	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("%w: image exists exited with status %d", ErrCommandFailed, result.ExitCode)
	}
}

// parseImageID validates Podman's immutable image ID output and removes its
// optional digest prefix. It preserves hexadecimal letter case.
func parseImageID(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	stripped := strings.TrimPrefix(trimmed, "sha256:")
	if len(stripped) != 64 {
		return "", fmt.Errorf("%w: image ID must be 64 hex characters, got %q", ErrInvalidResponse, stripped)
	}

	if !imageIDPattern.MatchString(stripped) {
		return "", fmt.Errorf("%w: image ID contains invalid characters, got %q", ErrInvalidResponse, stripped)
	}

	return stripped, nil
}

// ImageID resolves a local image reference to its immutable 64-digit hex ID.
// It does not pull an image or create a container. On failure it returns an empty
// ID and an error. Invalid ID output wraps ErrInvalidResponse.
func (c *Client) ImageID(ctx context.Context, image string) (string, error) {
	if err := validateImageReference(image); err != nil {
		return "", err
	}
	_, err := c.Preflight(ctx, runtime.NetworkNone)
	if err != nil {
		return "", err
	}
	result, err := c.checked(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return "", err
	}

	return parseImageID(result.Stdout)
}
