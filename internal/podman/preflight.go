package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"

	"go-sesh/internal/runtime"
)

// These structs model only the Podman fields this adapter uses. Unknown engine
// fields are allowed because Podman adds information independently of our schema.
type hostJSON struct {
	Host struct {
		Security struct {
			// A pointer distinguishes omitted/null data from an explicit boolean.
			// Rootless operation requires positive evidence, not a default value.
			Rootless       *bool `json:"rootless"`
			SELinuxEnabled bool  `json:"selinuxEnabled"`
		} `json:"security"`
		ServiceIsRemote    bool     `json:"serviceIsRemote"`
		CgroupVersion      string   `json:"cgroupVersion"`
		CgroupControllers  []string `json:"cgroupControllers"`
		RootlessNetworkCmd string   `json:"rootlessNetworkCmd"`
	} `json:"host"`
	Version struct {
		Version string `json:"Version"`
		OsArch  string `json:"OsArch"`
	} `json:"version"`
}

// Preflight checks the local rootless engine and required resource controllers.
// It reads host information only; it never pulls an image or changes a container.
func (c *Client) Preflight(ctx context.Context, network runtime.NetworkMode) (runtime.HostInfo, error) {
	if err := network.Validate(); err != nil {
		return runtime.HostInfo{}, err
	}
	// This is the supported-host contract, not a limitation of Go's portability.
	if goruntime.GOOS != "linux" || goruntime.GOARCH != "amd64" {
		return runtime.HostInfo{}, fmt.Errorf("%w: Linux x86-64 is required", ErrUnsupportedHost)
	}
	result, err := c.checked(ctx, "info", "--format", "json")
	if err != nil {
		return runtime.HostInfo{}, err
	}
	var info hostJSON
	if err := decodeResponse(result.Stdout, &info); err != nil {
		return runtime.HostInfo{}, err
	}
	return validateHost(info, network)
}

func validateHost(info hostJSON, network runtime.NetworkMode) (runtime.HostInfo, error) {
	if !versionAtLeast(info.Version.Version, "6.1.1") {
		return runtime.HostInfo{}, fmt.Errorf("%w: Podman 6.1.1 or later is required", ErrUnsupportedHost)
	}
	// Rootless and local are separate requirements. A remote rootless service
	// still falls outside the host ownership and filesystem assumptions here.
	if info.Host.Security.Rootless == nil || !*info.Host.Security.Rootless || info.Host.ServiceIsRemote {
		return runtime.HostInfo{}, fmt.Errorf("%w: a local rootless engine is required", ErrUnsupportedHost)
	}
	// Limit flags alone are not enough. The engine must report the controllers
	// needed to enforce memory and process-count limits for rootless sessions.
	if info.Host.CgroupVersion != "v2" || !slices.Contains(info.Host.CgroupControllers, "memory") || !slices.Contains(info.Host.CgroupControllers, "pids") {
		return runtime.HostInfo{}, fmt.Errorf("%w: cgroup v2 with memory and PID controllers is required", ErrUnsupportedHost)
	}
	// Do not require a networking backend for isolated sessions or inspection.
	// Check pasta only when the caller explicitly requests that network mode.
	if network == runtime.NetworkPasta && !strings.Contains(strings.ToLower(info.Host.RootlessNetworkCmd), "pasta") {
		return runtime.HostInfo{}, fmt.Errorf("%w: rootless pasta networking is unavailable", ErrUnsupportedHost)
	}
	arch := info.Version.OsArch
	if arch == "" {
		arch = "linux/amd64"
	}
	return runtime.HostInfo{Version: info.Version.Version, ImageArch: arch, SELinuxEnabled: info.Host.Security.SELinuxEnabled}, nil
}

var versionCore = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*`)

// Like the TypeScript reference, compare the numeric prefix and allow a build
// suffix. Parse failures fail closed rather than accepting a partial number.
func versionAtLeast(actual, minimum string) bool {
	left := strings.Split(versionCore.FindString(actual), ".")
	right := strings.Split(minimum, ".")
	// Compare numeric components, not strings: 6.10 is newer than 6.2. Missing
	// trailing components are zero, so 6.2 and 6.2.0 compare equally.
	size := max(len(left), len(right))
	a, b := make([]uint64, size), make([]uint64, size)
	for i, part := range left {
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		a[i] = value
	}
	for i, part := range right {
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		b[i] = value
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func decodeResponse(output string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(output))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	// Decode stops after one value. Require EOF next so trailing objects or
	// garbage cannot be silently ignored as part of an otherwise valid response.
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: expected one JSON value", ErrInvalidResponse)
	}
	return nil
}
