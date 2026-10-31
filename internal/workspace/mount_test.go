package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareMount(t *testing.T) {
	source := t.TempDir()
	marker := filepath.Join(source, "source.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, alias, filepath.Join(source, ".")} {
		got, err := PrepareMount(context.Background(), path)
		if err != nil || got != source {
			t.Fatalf("PrepareMount(%q) = %q, %v, want %q", path, got, err, source)
		}
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("preparation modified source: %q, %v", data, err)
	}
}

func TestMountBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	private := filepath.Join(home, ".ssh")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "credentials-alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	for _, path := range []string{"/", home, private, alias, runtimeDir} {
		if got, err := PrepareMount(context.Background(), path); got != "" || err == nil {
			t.Fatalf("unsafe mount %q returned %q, %v", path, got, err)
		}
	}
	project := filepath.Join(home, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareMount(context.Background(), project); err != nil {
		t.Fatalf("ordinary project below home was refused: %v", err)
	}
}

func TestPrepareMountFailures(t *testing.T) {
	source := t.TempDir()
	file := filepath.Join(source, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	badVolume := filepath.Join(source, "path,with-comma")
	if err := os.Mkdir(badVolume, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "bad\x00path", file, filepath.Join(source, "missing"), badVolume} {
		if got, err := PrepareMount(context.Background(), path); got != "" || err == nil {
			t.Fatalf("PrepareMount(%q) = %q, %v, want empty path and error", path, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareMount(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation = %v", err)
	}
}
