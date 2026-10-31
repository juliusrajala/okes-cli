package podman

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go-sesh/internal/runtime"
)

// Keep the alias format aligned with session.ValidateName without making the
// engine adapter depend on the session manager or store.
var createNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

var containerEnvironmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

var reservedContainerEnvironmentName = regexp.MustCompile(`^(?:` +
	`HOME|PATH|PWD|OLDPWD|TERM|COLORTERM|LANG|LC_ALL|` +
	`NODE_OPTIONS|BASH_ENV|ENV|SHELLOPTS|NODE_TLS_REJECT_UNAUTHORIZED|` +
	`NODE_EXTRA_CA_CERTS|SSL_CERT_(?:FILE|DIR)|HTTP_PROXY|HTTPS_PROXY|ALL_PROXY|NO_PROXY|` +
	`XDG_.*|PI_.*|LD_.*|OPENAI_.*|ANTHROPIC_.*|GOOGLE_GENERATIVE_AI_.*|` +
	`AZURE_OPENAI_.*|AWS_ENDPOINT_URL.*|GIT_CONFIG_.*|GIT_ASKPASS|` +
	`GIT_SSH(?:_COMMAND)?|GIT_EXEC_PATH|GIT_EXTERNAL_DIFF|GIT_PAGER|` +
	`SSH_ASKPASS|SSH_AUTH_SOCK)$`)

var secretContainerEnvironmentName = regexp.MustCompile(`(?i)(?:^|_)(?:API_KEY|ACCESS_KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|AUTH|DATABASE_URL)(?:_|$)`)

const (
	maxContainerEnvironmentEntries    = 64
	maxContainerEnvironmentValueBytes = 4096
	maxContainerEnvironmentTotalBytes = 16 * 1024
)

// validateCreateRequest validates creation data and returns sorted custom
// container environment entries. It does not resolve paths, access files, check
// image availability, or run Podman. The future invocation path must complete
// workspace preparation and rootless preflight before creating anything.
func validateCreateRequest(request runtime.CreateRequest, host createHost) ([]string, error) {
	if err := validateTarget(request.Target); err != nil {
		return nil, err
	}
	if request.Target.ContainerID != "" {
		return nil, fmt.Errorf("create request must not contain an existing container ID")
	}
	if !createNamePattern.MatchString(request.Name) {
		return nil, fmt.Errorf("invalid session name for container creation")
	}
	if !imageIDPattern.MatchString(request.ImageID) {
		return nil, fmt.Errorf("create request requires an immutable 64-digit hexadecimal image ID")
	}
	if !filepath.IsAbs(request.WorkspacePath) || filepath.Clean(request.WorkspacePath) != request.WorkspacePath || strings.ContainsAny(request.WorkspacePath, "\x00:,") {
		return nil, fmt.Errorf("workspace path must be clean and absolute without NUL, ':' or ','")
	}
	if err := request.WorkspaceMode.Validate(); err != nil {
		return nil, err
	}
	if err := request.Network.Validate(); err != nil {
		return nil, err
	}
	if host.UID < 0 || host.GID < 0 {
		return nil, fmt.Errorf("mapped host UID/GID must be non-negative")
	}
	if len(request.Command) == 0 {
		return nil, fmt.Errorf("container command must contain at least one argument")
	}
	for i, arg := range request.Command {
		if arg == "" || strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("container command argument %d must be nonempty and contain no NUL bytes", i)
		}
	}
	if request.TTY && (strings.ContainsRune(host.Term, '\x00') || strings.ContainsRune(host.ColorTerm, '\x00')) {
		return nil, fmt.Errorf("terminal environment must contain no NUL bytes")
	}
	return containerEnvironment(request.Environment)
}

// containerEnvironment applies the TypeScript container-environment policy.
// Values remain literal, including '$', quotes, and '='. This is not the allowlist
// used for the host Podman process. Never include variable values in errors.
func containerEnvironment(environment map[string]string) ([]string, error) {
	if len(environment) > maxContainerEnvironmentEntries {
		return nil, fmt.Errorf("container environment has more than %d entries", maxContainerEnvironmentEntries)
	}
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	slices.Sort(names)
	entries := make([]string, 0, len(names))
	total := 0
	for _, name := range names {
		if !containerEnvironmentName.MatchString(name) {
			return nil, fmt.Errorf("invalid container environment variable name")
		}
		if reservedContainerEnvironmentName.MatchString(name) || secretContainerEnvironmentName.MatchString(name) {
			return nil, fmt.Errorf("container environment cannot set reserved or credential-like variable %s", name)
		}
		value := environment[name]
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("container environment value for %s must be a single-line literal string", name)
		}
		// Go string length counts bytes, matching the reference's UTF-8 limits.
		if len(value) > maxContainerEnvironmentValueBytes {
			return nil, fmt.Errorf("container environment value for %s exceeds %d bytes", name, maxContainerEnvironmentValueBytes)
		}
		total += len(name) + len(value) + 1
		if total > maxContainerEnvironmentTotalBytes {
			return nil, fmt.Errorf("container environment exceeds %d bytes", maxContainerEnvironmentTotalBytes)
		}
		entries = append(entries, name+"="+value)
	}
	return entries, nil
}
