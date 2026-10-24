package podman

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHostEnvironmentAllowlist(t *testing.T) {
	env, _, _, err := buildHostEnvironment([]string{
		"HOME=/home/example", "XDG_RUNTIME_DIR=/run/user/1000", "LANG=C.UTF-8",
		"TERM=xterm-256color", "OPENAI_API_KEY=do-not-forward", "HTTP_PROXY=http://proxy",
		"BASH_ENV=/untrusted/script", "LD_PRELOAD=/untrusted/library",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"HOME=/home/example", "LANG=C.UTF-8", "TERM=xterm-256color"} {
		if !slices.Contains(env, entry) {
			t.Errorf("missing allowed entry %q", entry)
		}
	}
	for _, entry := range env {
		for _, forbidden := range []string{"OPENAI_", "HTTP_PROXY=", "BASH_ENV=", "LD_"} {
			if strings.HasPrefix(entry, forbidden) {
				t.Errorf("unexpected forwarded entry %q", entry)
			}
		}
	}
}

func TestHostEnvironmentRejectsOverrides(t *testing.T) {
	for _, entry := range []string{
		"CONTAINER_HOST=ssh://remote", "CONTAINER_CONNECTION=remote", "DOCKER_HOST=unix:///socket",
		"CONTAINERS_CONF=", "CONTAINERS_STORAGE_CONF=/tmp/storage.conf", "HOME=relative",
		"XDG_CONFIG_HOME=relative", "TMPDIR=relative", "BAD", "=bad", "HOME=/bad\x00",
	} {
		t.Run(entry, func(t *testing.T) {
			if _, _, _, err := buildHostEnvironment([]string{entry}, ""); err == nil {
				t.Fatal("unsafe source environment accepted")
			}
		})
	}
}

func TestHostEnvironmentFiltersProjectPaths(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	trusted := filepath.Join(root, "trusted")
	for _, path := range []string{project, trusted} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	env, search, canonical, err := buildHostEnvironment([]string{"PATH=.:relative:" + project + ":" + alias + ":" + trusted + ":/nonexistent-go-sesh-bin"}, project)
	if err != nil {
		t.Fatal(err)
	}
	if search != trusted || canonical != project || !slices.Contains(env, "PATH="+trusted) {
		t.Fatalf("filtered PATH = %q, project = %q, env = %q", search, canonical, env)
	}
}

func TestResolveExecutableRejectsProjectSymlink(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	trusted := filepath.Join(root, "trusted")
	fallback := filepath.Join(root, "fallback")
	for _, path := range []string{project, trusted, fallback} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	projectExecutable := filepath.Join(project, "podman")
	if err := os.WriteFile(projectExecutable, []byte("not executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(projectExecutable, filepath.Join(trusted, "podman")); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(fallback, "podman")
	if err := os.WriteFile(good, []byte("also not executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := resolveExecutable(trusted+":"+fallback, project)
	if err != nil || got != good {
		t.Fatalf("executable = %q, error = %v, want %q", got, err, good)
	}
}
