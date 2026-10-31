package podman

import (
	"fmt"
	"strings"

	"go-sesh/internal/runtime"
)

// createHost holds trusted host facts. Later invocation code must obtain UID/GID
// from the invoking process and SELinux/terminal values from the checked host.
// These fields must never come from project configuration.
type createHost struct {
	UID            int
	GID            int
	SELinuxEnabled bool
	Term           string
	ColorTerm      string
}

// buildCreateArgs builds Podman's literal create argv without running a process.
// Its result starts with "create". Client.invoke adds the global --remote=false.
// On failure it returns nil and an error. It must not modify either input.
func buildCreateArgs(request runtime.CreateRequest, host createHost) ([]string, error) {
	environment, err := validateCreateRequest(request, host)
	if err != nil {
		return nil, err
	}

	args := createIdentityArgs(request)
	args = append(args, createPolicyArgs(request.Network, host)...)
	args = append(args, createWorkspaceArgs(request.WorkspacePath, host.SELinuxEnabled)...)
	args = append(args, createEnvironmentArgs(request, environment)...)
	args = append(args, createTerminalArgs(request, host)...)
	args = append(args, createWorkloadArgs(request.Command, request.ImageID)...)
	return args, nil
}

// The section helpers accept inputs checked by validateCreateRequest. They
// return fresh slices and do not run processes, mutate inputs, or revalidate them.
func createIdentityArgs(request runtime.CreateRequest) []string {
	return []string{
		"create",
		"--name", request.Target.ContainerName,
		"--label", ownerLabel + "=pi-sesh",
		"--label", sessionLabel + "=" + request.Target.SessionID,
		"--label", "io.pi-sesh.session-name=" + request.Name,
	}
}

func createPolicyArgs(network runtime.NetworkMode, host createHost) []string {
	return []string{
		"--pull=never",
		"--image-volume=ignore",
		"--http-proxy=false",
		"--network", string(network),
		"--userns=keep-id",
		"--user", fmt.Sprintf("%d:%d", host.UID, host.GID),
		"--read-only",
		"--cap-drop=all",
		"--security-opt=no-new-privileges",
		"--memory=4g",
		"--memory-swap=4g",
		"--cpus=4",
		"--pids-limit=512",
		"--tmpfs", "/home/session:rw,noexec,nosuid,nodev,size=1g,mode=1777",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=512m,mode=1777",
	}
}

// createWorkspaceArgs maps the validated host workspace to /workspace. SELinux
// requires private relabeling; the host path remains one literal argv value.
func createWorkspaceArgs(workspacePath string, selinux bool) []string {
	volume := workspacePath + ":/workspace:rw"
	if selinux {
		volume += ",Z"
	}
	return []string{"--volume", volume, "--workdir", "/workspace"}
}

func createEnvironmentArgs(request runtime.CreateRequest, environment []string) []string {
	args := []string{
		"--env", "HOME=/home/session",
		"--env", "XDG_CONFIG_HOME=/home/session/.config",
		"--env", "XDG_DATA_HOME=/home/session/.local/share",
		"--env", "XDG_CACHE_HOME=/home/session/.cache",
		"--env", "LANG=C.UTF-8",
		"--env", "PI_SESH_SESSION=" + request.Name,
		"--env", "PI_SESH_SESSION_ID=" + request.Target.SessionID,
		"--env", "PI_SESH_NETWORK=" + string(request.Network),
		"--env", "PI_SESH_WORKSPACE=" + string(request.WorkspaceMode),
	}
	// The entry point supplies sorted, validated KEY=value strings, not a map.
	for _, entry := range environment {
		args = append(args, "--env", entry)
	}
	return args
}

func createTerminalArgs(request runtime.CreateRequest, host createHost) []string {
	var args []string
	if request.Interactive || request.TTY {
		args = append(args, "--interactive")
	}
	if request.TTY {
		args = append(args, "--tty")
		if host.Term != "" {
			args = append(args, "--env", "TERM="+host.Term)
		}
		if host.ColorTerm != "" {
			args = append(args, "--env", "COLORTERM="+host.ColorTerm)
		}
	}
	return args
}

// createWorkloadArgs consumes validated nonempty argv without changing or sharing
// its slice storage. This section must remain last in the complete create argv.
func createWorkloadArgs(command []string, imageID string) []string {
	prefix := []string{
		"--entrypoint", command[0],
		imageID,
	}

	return append(prefix, command[1:]...)
}

// parseContainerID validates create stdout and normalizes the ID to lowercase.
// It rejects the storage pending marker and returns an empty ID with an error
// wrapping ErrInvalidResponse for invalid output.
func parseContainerID(output string) (string, error) {
	trimmed := strings.TrimSpace(output)

	if matched := containerIDPattern.MatchString(trimmed); !matched || trimmed == strings.Repeat("0", 64) {
		return "", fmt.Errorf("%w: container ID must be 64 hex characters, got %q", ErrInvalidResponse, trimmed)
	}

	return strings.ToLower(trimmed), nil
}
