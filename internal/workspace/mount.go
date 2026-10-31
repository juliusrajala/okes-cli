package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PrepareMount resolves a caller-selected directory for a read-write workspace.
// It does not create, copy, relabel, or delete the directory. The caller retains
// ownership. Like the reference, symlinks are resolved before mounting.
func PrepareMount(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if path == "" || strings.ContainsRune(path, '\x00') {
		return "", fmt.Errorf("workspace path must be nonempty and contain no NUL bytes")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve workspace directory: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect workspace directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace must be an existing directory")
	}
	if strings.ContainsAny(canonical, ":,") {
		return "", fmt.Errorf("workspace paths containing ':' or ',' are unsupported by Podman volume syntax")
	}
	if err := checkMountBoundary(canonical); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return canonical, nil
}

// A caller selects the workspace explicitly, but it must not become a broad
// host-root, home, credential, or engine-state mount. This enforces the documented
// boundary rather than relying on the path merely being a directory.
func checkMountBoundary(path string) error {
	for _, root := range []string{"/", "/home", "/root", "/etc", "/proc", "/sys", "/dev", "/run", "/var", "/tmp", "/usr", "/usr/local", "/opt", "/mnt", "/media"} {
		if path == root {
			return fmt.Errorf("workspace must not expose a broad host or system path")
		}
	}
	for _, privateTree := range []string{"/proc", "/sys", "/dev", "/run"} {
		if strings.HasPrefix(path, privateTree+string(filepath.Separator)) {
			return fmt.Errorf("workspace must not expose host kernel, device, or runtime state")
		}
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtimeDir) {
		if resolved, err := filepath.EvalSymlinks(runtimeDir); err == nil {
			runtimeDir = resolved
		}
		if path == runtimeDir || strings.HasPrefix(path, runtimeDir+string(filepath.Separator)) || strings.HasPrefix(runtimeDir, path+string(filepath.Separator)) {
			return fmt.Errorf("workspace must not expose host runtime sockets or credentials")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot establish host-home mount boundary: %w", err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return fmt.Errorf("resolve host-home mount boundary: %w", err)
	}
	if path == home {
		return fmt.Errorf("workspace must not expose the host home directory")
	}
	for _, relative := range []string{".ssh", ".gnupg", ".pi", ".aws", ".kube", ".azure", ".docker", ".password-store", ".config/containers", ".config/gcloud", ".config/op", ".local/share/keyrings", ".local/share/containers"} {
		private := filepath.Join(home, relative)
		if resolved, err := filepath.EvalSymlinks(private); err == nil {
			private = resolved
		}
		if path == private || strings.HasPrefix(path, private+string(filepath.Separator)) || strings.HasPrefix(private, path+string(filepath.Separator)) {
			return fmt.Errorf("workspace must not expose credentials or container-engine state")
		}
	}
	return nil
}
