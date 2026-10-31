# Container creation boundary

## Current implementation

`runtime.CreateRequest` describes the inputs to container creation. `podman.validateCreateRequest` validates those inputs without accessing the filesystem or engine. It returns sorted custom container environment entries through `podman.containerEnvironment`.

`buildCreateArgs` composes identity, fixed policy, workspace, environment, terminal, and workload sections through small pure helpers. `parseContainerID` validates create stdout. Request validation, complete argv construction, individual sections, and output parsing are implemented and tested. Concrete Podman creation and detached start invocation are implemented and tested with fake process runners. The surrounding manager lifecycle is tested with a fake runtime. The gated real Podman smoke test passed on local rootless Podman 6.1.4 with the dedicated `localhost/go-sesh-smoke:1` image.

Sources:

- [runtime/create.go](../internal/runtime/create.go): neutral request data.
- [podman/create.go](../internal/podman/create.go): trusted host settings, argv composition, and container-ID output parsing.
- [podman/create_test.go](../internal/podman/create_test.go): active tests for complete argv construction, individual sections, and output parsing.
- [podman/create_validate.go](../internal/podman/create_validate.go): request and container-environment validation.
- [podman/create_validate_test.go](../internal/podman/create_validate_test.go): active validation tests.

## Request and trust boundaries

The request carries session identity, alias, immutable image ID, prepared workspace path/mode, explicit network mode, workload argv, explicit container environment, and terminal/stdin selection.

- `Target.ContainerID` must be empty before creation. The all-zero pending marker belongs in recovery storage, not a runtime request.
- The session layer validates the UUID and reserves the alias. The adapter checks agreement between the target's session ID and container name. It also checks the alias format.
- `ImageID` must already contain 64 hexadecimal characters. A tag or `sha256:`-prefixed value is not a resolved request ID. Input validation does not prove local image availability.
- `WorkspacePath` is a host path selected by the caller. Project configuration must not select arbitrary host mount sources.
- Workspace paths must be clean absolute paths without NUL, `:`, or `,`. This is a syntax check, not proof of existence, canonical filesystem identity, or ownership.
- Before a real invocation, workspace preparation must resolve the selected path to an existing canonical directory. A copy workspace must already have its ownership metadata. Validation does not create copies or authorize deletion of a source directory.
- Network and workspace modes require explicit values. Defaults belong at the input boundary. `clone` and host networking are rejected.
- Every workload argument must be nonempty and contain no NUL bytes. Spaces and shell characters remain literal argv data. This follows the TypeScript start-command contract; it does not add exec's separate argument-count limit.
- UID/GID, SELinux state, and terminal environment are trusted host facts. They do not come from project configuration.
- The request has no arbitrary Podman flags, extra host mounts, or host preparation commands.
- Callers must not modify request slices or maps during an operation. Validation does not mutate them.

The request is not a session record or a JSON schema. This slice makes no persistence changes.

## Container environment

The container environment is separate from the restricted environment used to run the host Podman process. It is not a copy of `os.Environ()`.

The custom environment follows the TypeScript policy:

| Rule | Limit or behavior |
| --- | --- |
| Variable name | 1–64 uppercase ASCII letters, digits, or `_`; first character is a letter or `_` |
| Entry count | At most 64 custom entries |
| Value size | At most 4096 UTF-8 bytes per value |
| Total size | At most 16 KiB, counting each name, `=`, and value |
| Value format | Literal single-line strings without NUL; empty values are allowed |
| Output order | Sorted by variable name |
| Error messages | Do not include values |

Protected names include the runtime's HOME/XDG/session variables, execution hooks, loader settings, proxies, provider settings, Git/SSH execution settings, and credential-like names. For the exact matching rules, see `containerEnvironment` and its patterns in the source.

Literal `$`, quotes, `=`, and shell characters are not expanded. Terminal values are separate trusted host inputs. They will be forwarded only for a TTY request.

## Command construction

`buildCreateArgs` returns an argv slice beginning with `create`. It does not execute Podman. `Client.invoke` adds `--remote=false` when it invokes an engine operation.

```text
buildCreateArgs(request, trustedHostSettings)
  validateCreateRequest(request, trustedHostSettings)
    validate target, alias, image ID, path syntax, modes, argv, UID/GID
    containerEnvironment(request.Environment)
      validate and sort custom KEY=value entries
    failure -> return nil argv and error
  append createIdentityArgs(request)
  append createPolicyArgs(request.Network, trustedHostSettings)
  append createWorkspaceArgs(path, SELinux state)
  append createEnvironmentArgs(request, validated entries)
  append createTerminalArgs(request, trustedHostSettings)
  append createWorkloadArgs(command, image ID)
  return argv
```

Validation runs once at the entry point. The section helpers consume validated inputs and return independently owned slices. They do not repeat validation or run processes. The workload section must remain last so later runtime flags cannot become workload arguments.

The fixed policy preserves the reference behavior:

- Ownership, session-ID, and session-name labels.
- `--pull=never`, `--image-volume=ignore`, and `--http-proxy=false`.
- Explicit `none` or `pasta` networking.
- `--userns=keep-id` and the invoking host UID/GID.
- A read-only root filesystem, all capabilities dropped, and `no-new-privileges`.
- A 4 GiB memory limit, a 4 GiB combined memory/swap limit, four CPUs, and a PID limit of 512.
- Writable tmpfs at `/home/session` and `/tmp` with `noexec,nosuid,nodev` and the reference size limits.
- One explicit read-write workspace volume at `/workspace`, with private `Z` relabeling only when SELinux is enabled.
- Explicit HOME/XDG/session environment without automatic host-environment forwarding.
- Literal workload argv through the entrypoint and image position, not a generated shell command.

`parseContainerID` trims stdout, requires a real 64-character hexadecimal ID, and normalizes it to lowercase. It rejects the all-zero pending marker, image-style `sha256:` prefixes, short IDs, multiple lines of IDs, and trailing data. Invalid output wraps `podman.ErrInvalidResponse`. Rejecting the pending marker is consistent with the existing Go inspection boundary.

## Requirements before live creation

`Manager.Start` preflights the requested network, resolves the local image, prepares the canonical workspace directory, and saves a pending recovery record before requesting creation. `prepareCreate` validates inputs, rechecks that the workspace still resolves to the recorded path, and constructs argv with trusted host UID/GID and preflight SELinux state. It refuses live copy creation until copy ownership exists.

A failed create command does not prove that no container was created. Recovery must use the reserved session name and ownership labels when an ID is unavailable. Later cleanup must verify ownership and absence before removing owned host data. Neither successful input validation nor parsed output alone establishes runtime ownership.

Recovery orchestration, mutation locking, metadata deletion, and mounted-workspace preparation are implemented by the [mounted-session lifecycle](mounted-session.md). `Client.Create` now executes prepared argv and validates its returned ID. `Client.StartDetached` verifies a created container, pins its ID, starts it, and returns a fresh non-missing observation. Invocation failures remain errors for the manager's recovery path. Resources, copy provisioning, and the CLI remain pending.
