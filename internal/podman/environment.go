package podman

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// buildHostEnvironment returns Podman's host environment, its executable search
// path, and the canonical project path. It does not build a workload environment.
func buildHostEnvironment(source []string, projectDir string) ([]string, string, string, error) {
	// Split at the first '=' so values can contain '='. Later duplicate names
	// replace earlier values, as they do in Go's subprocess environment handling.
	input := make(map[string]string)
	for _, entry := range source {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" || strings.ContainsRune(entry, '\x00') {
			return nil, "", "", fmt.Errorf("invalid source environment entry")
		}
		input[name] = value
	}
	// Reject overrides rather than silently ignoring a request for another engine.
	// CONTAINERS_* can also redirect Podman's host configuration or storage.
	for _, name := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "DOCKER_HOST"} {
		if input[name] != "" {
			return nil, "", "", fmt.Errorf("remote container setting %s is unsupported", name)
		}
	}
	for name := range input {
		if strings.HasPrefix(name, "CONTAINERS_") {
			return nil, "", "", fmt.Errorf("Podman environment override %s is unsupported", name)
		}
	}

	// Compare real filesystem paths, not aliases that can hide project directories
	// behind symlinks. An empty project disables this additional exclusion.
	project := ""
	if projectDir != "" {
		absolute, err := filepath.Abs(projectDir)
		if err != nil {
			return nil, "", "", err
		}
		project, err = filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, "", "", fmt.Errorf("resolve selected project: %w", err)
		}
	}
	// Start from an allowlist, not a copy with known secrets removed. Unknown
	// credential, proxy, and execution-hook variables must not reach Podman.
	// HOME/XDG support the host engine; forwarding them does not mount host files.
	allowed := make(map[string]string)
	for _, name := range []string{
		"PATH", "HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"XDG_CACHE_HOME", "TMPDIR", "LANG", "LC_ALL", "TERM", "COLORTERM",
	} {
		if value, ok := input[name]; ok {
			allowed[name] = value
		}
	}
	// Relative configuration or storage paths could resolve against an untrusted
	// working directory. Require their meaning to be independent of that directory.
	for _, name := range []string{"HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "TMPDIR"} {
		if value, ok := allowed[name]; ok && !filepath.IsAbs(value) {
			return nil, "", "", fmt.Errorf("%s must be absolute", name)
		}
	}
	rawPath, ok := allowed["PATH"]
	if !ok {
		rawPath = "/usr/bin:/bin"
	}
	// Exclude relative entries, including '.', and project-controlled directories.
	// Otherwise a project could supply the host podman executable or its helpers.
	var paths []string
	for _, entry := range filepath.SplitList(rawPath) {
		if !filepath.IsAbs(entry) {
			continue
		}
		canonical, err := filepath.EvalSymlinks(entry)
		if err != nil || (project != "" && within(project, canonical)) {
			continue
		}
		info, err := os.Stat(canonical)
		if err == nil && info.IsDir() {
			paths = append(paths, canonical)
		}
	}
	searchPath := strings.Join(paths, string(os.PathListSeparator))
	if searchPath == "" {
		// Use fixed system paths rather than restoring the rejected source PATH.
		searchPath = "/usr/bin:/bin"
	}
	allowed["PATH"] = searchPath
	var env []string
	for name, value := range allowed {
		env = append(env, name+"="+value)
	}
	// Go map iteration is unordered. Stable ordering makes invocations and tests
	// reproducible without changing the environment's meaning.
	slices.Sort(env)
	return env, searchPath, project, nil
}

// resolveExecutable uses the filtered PATH. exec.LookPath("podman") would use
// the Go process's own PATH instead, bypassing that filtering.
func resolveExecutable(searchPath, project string) (string, error) {
	for _, directory := range filepath.SplitList(searchPath) {
		// A trusted directory can still contain a symlink into the project.
		// Check the executable's destination as well as the PATH directory.
		path, err := filepath.EvalSymlinks(filepath.Join(directory, "podman"))
		if err != nil || (project != "" && within(project, path)) {
			continue
		}
		info, err := os.Stat(path)
		// 0111 selects the owner/group/other execute bits. This is a candidate
		// check; the OS still decides whether our user can execute it at startup.
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("Podman is required in a trusted absolute PATH directory")
}

// within compares path components. A raw string prefix would incorrectly treat
// a sibling such as /work/project-tools as part of /work/project.
func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}
