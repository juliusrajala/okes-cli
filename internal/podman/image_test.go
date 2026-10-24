package podman

import (
	"context"
	"errors"
	"go-sesh/internal/process"
	"strconv"
	"strings"
	"testing"
)

func TestValidateImageReference(t *testing.T) {
	for _, image := range []string{"localhost/worker:1", "worker", "registry.example/worker@sha256:" + strings.Repeat("a", 64)} {
		if err := validateImageReference(image); err != nil {
			t.Fatal(err)
		}
	}
	for _, image := range []string{"", "--remote=true", "-pull", "hello world", "$(whoami)", "bad\x00", strings.Repeat("a", 256)} {
		if err := validateImageReference(image); err == nil {
			t.Errorf("unsafe image reference accepted: %q", image)
		}
	}
}

func TestImageExists(t *testing.T) {
	for _, status := range []int{0, 1, 2, 125} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			client := scriptClient(t, hostStep(), runnerStep{args: []string{"image", "exists", "localhost/worker:1"}, result: process.Result{ExitCode: status}})
			got, err := client.ImageExists(context.Background(), "localhost/worker:1")
			if status == 0 && (err != nil || !got) {
				t.Fatalf("exists = %t, error = %v, want true, nil", got, err)
			}
			if status == 1 && (err != nil || got) {
				t.Fatalf("exists = %t, error = %v, want false, nil", got, err)
			}
			if status != 0 && status != 1 && (!errors.Is(err, ErrCommandFailed) || got) {
				t.Fatalf("exists = %t, error = %v, want command failure", got, err)
			}
		})
	}
	t.Run("execution failure is not absence", func(t *testing.T) {
		cause := errors.New("engine unavailable")
		client := scriptClient(t, hostStep(), runnerStep{args: []string{"image", "exists", "localhost/worker:1"}, err: cause})
		got, err := client.ImageExists(context.Background(), "localhost/worker:1")
		if got || !errors.Is(err, cause) {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
	t.Run("preflight failure prevents image probe", func(t *testing.T) {
		step := hostStep()
		step.result.Stdout = strings.Replace(testHostJSON, `"rootless":true`, `"rootless":false`, 1)
		client := scriptClient(t, step)
		got, err := client.ImageExists(context.Background(), "localhost/worker:1")
		if got || !errors.Is(err, ErrUnsupportedHost) {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
	t.Run("invalid image prevents process execution", func(t *testing.T) {
		client := scriptClient(t)
		if got, err := client.ImageExists(context.Background(), "--remote=true"); got || err == nil {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
	t.Run("cancelled probe", func(t *testing.T) {
		client := scriptClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := client.ImageExists(ctx, "localhost/worker:1")
		if got || !errors.Is(err, context.Canceled) {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
}
