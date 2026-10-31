package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-sesh/internal/podman"
	"go-sesh/internal/runtime"
	"go-sesh/internal/session"
)

// This test requires explicit operator opt-in and an existing local image with
// /bin/sh and sleep. It never pulls or builds images. Do not use t.TempDir for
// session state: automatic cleanup would discard recovery data on uncertainty.
func TestMountedSession(t *testing.T) {
	if os.Getenv("PI_SESH_PODMAN_TESTS") != "1" {
		t.Skip("set PI_SESH_PODMAN_TESTS=1 and PI_SESH_TEST_IMAGE to opt into live Podman")
	}
	image := os.Getenv("PI_SESH_TEST_IMAGE")
	if image == "" {
		t.Fatal("PI_SESH_TEST_IMAGE must name an existing local image with /bin/sh and sleep")
	}
	root, err := os.MkdirTemp("", "go-sesh-live-")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "workspace")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := session.OpenStore(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var manager *session.Manager
	preserve := false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		safe := !preserve
		if manager != nil && safe {
			records, err := store.List(ctx)
			if err != nil {
				t.Errorf("read recovery state: %v", err)
				safe = false
			}
			for _, record := range records {
				view, err := manager.Inspect(ctx, record.ID)
				if err == nil && view.Inspection.State == runtime.Running {
					_, err = manager.Stop(ctx, record.ID)
				}
				if err == nil {
					err = manager.Remove(ctx, record.ID)
				}
				if err != nil {
					t.Errorf("verified test cleanup: %v", err)
					safe = false
				}
			}
		}
		if err := store.Close(); err != nil {
			t.Errorf("close test store: %v", err)
			safe = false
		}
		if !safe {
			t.Logf("Recovery data and workspace retained at %s", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove verified test files: %v", err)
		}
	})
	client, err := podman.New(podman.Options{ProjectDir: source})
	if err != nil {
		t.Fatal(err)
	}
	manager = &session.Manager{Store: store, Runtime: client}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	view, err := manager.Start(ctx, session.StartRequest{
		Name: "live-smoke", Image: image, WorkspacePath: source,
		Command: []string{"/bin/sh", "-c", "printf 'mounted\\n' > /workspace/result.txt; exec sleep 300"},
	})
	if err != nil {
		var startup *session.StartError
		preserve = errors.As(err, &startup) && startup.RecoveryRequired
		t.Fatal(err)
	}
	if view.Inspection.State != runtime.Running {
		t.Fatalf("expected running workload, got %+v", view.Inspection)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(source, "result.txt"))
		if err == nil && string(data) == "mounted\n" {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("workload did not write mounted workspace: %v", waitCtx.Err())
		case <-ticker.C:
		}
	}
	if _, err := manager.Stop(ctx, view.Record.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(ctx, view.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, view.Record.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("session metadata remains after removal: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(source, "result.txt")); err != nil || string(data) != "mounted\n" {
		t.Fatalf("session removal changed the source workspace: %q, %v", data, err)
	}
}
