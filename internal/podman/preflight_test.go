package podman

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
)

func TestPreflight(t *testing.T) {
	client := scriptClient(t, hostStep())
	info, err := client.Preflight(context.Background(), runtime.NetworkNone)
	if err != nil || info.Version != "6.1.1" || info.ImageArch != "linux/amd64" || !info.SELinuxEnabled {
		t.Fatalf("host info = %+v, error = %v", info, err)
	}
}

func TestPreflightRejectsUnsupportedHost(t *testing.T) {
	tests := []struct {
		name, from, to string
	}{
		{name: "old version", from: `"6.1.1"`, to: `"6.1.0"`},
		{name: "invalid version", from: `"6.1.1"`, to: `"unknown"`},
		{name: "not rootless", from: `"rootless":true`, to: `"rootless":false`},
		{name: "missing rootless", from: `"rootless":true,`, to: ``},
		{name: "remote service", from: `"serviceIsRemote":false`, to: `"serviceIsRemote":true`},
		{name: "cgroup v1", from: `"cgroupVersion":"v2"`, to: `"cgroupVersion":"v1"`},
		{name: "no memory controller", from: `["memory","pids"]`, to: `["pids"]`},
		{name: "no PID controller", from: `["memory","pids"]`, to: `["memory"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := hostStep()
			step.result.Stdout = strings.Replace(testHostJSON, tt.from, tt.to, 1)
			client := scriptClient(t, step)
			if _, err := client.Preflight(context.Background(), runtime.NetworkNone); !errors.Is(err, ErrUnsupportedHost) {
				t.Fatalf("error = %v, want ErrUnsupportedHost", err)
			}
		})
	}
}

func TestPreflightNetwork(t *testing.T) {
	for _, network := range []runtime.NetworkMode{runtime.NetworkNone, runtime.NetworkPasta} {
		t.Run(string(network), func(t *testing.T) {
			step := hostStep()
			step.result.Stdout = strings.Replace(testHostJSON, `"pasta"`, `"slirp4netns"`, 1)
			client := scriptClient(t, step)
			_, err := client.Preflight(context.Background(), network)
			if network == runtime.NetworkNone && err != nil {
				t.Fatal(err)
			}
			if network == runtime.NetworkPasta && !errors.Is(err, ErrUnsupportedHost) {
				t.Fatalf("error = %v, want unsupported pasta", err)
			}
		})
	}
	client := scriptClient(t)
	if _, err := client.Preflight(context.Background(), runtime.NetworkMode("host")); err == nil {
		t.Fatal("unsupported network was accepted")
	}
}

func TestPreflightRejectsMalformedResponse(t *testing.T) {
	for _, output := range []string{`{`, `{} {}`, `[]`, `{"host":{"security":{"rootless":"true"}}}`} {
		t.Run(output, func(t *testing.T) {
			step := hostStep()
			step.result.Stdout = output
			client := scriptClient(t, step)
			if _, err := client.Preflight(context.Background(), runtime.NetworkNone); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestPreflightCommandFailure(t *testing.T) {
	client := scriptClient(t, runnerStep{args: []string{"info", "--format", "json"}, result: process.Result{ExitCode: 125}})
	if _, err := client.Preflight(context.Background(), runtime.NetworkNone); !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("error = %v, want ErrCommandFailed", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, version := range []string{"6.1.1", "6.1.1-dev", "6.2", "7", "6.10.0"} {
		if !versionAtLeast(version, "6.1.1") {
			t.Errorf("supported version rejected: %s", version)
		}
	}
	for _, version := range []string{"", "v6.1.1", "5.99.0", "6.1", "6.1.0", "999999999999999999999999999999"} {
		if versionAtLeast(version, "6.1.1") {
			t.Errorf("invalid or old version accepted: %s", version)
		}
	}
}
