# Behavior contract: TypeScript baseline and approved Go changes

This document records the current TypeScript behavior and the approved changes for the Go rewrite in this repository, go-sesh. The TypeScript implementation lives in `../pi-sesh/src/`.

The approved changes below come from the [updated rewrite plan](pi-sesh-go-rewrite-plan-updated.pdf). They are implementation targets, not existing Go capabilities. All later sections describe the TypeScript baseline, including its historical clone behavior. Preserve unrelated security and lifecycle guarantees.

## Approved Go changes: directory workspaces, not Git provisioning

### Workspace modes and ownership

- Go supports `mount` and filesystem `copy` instead of `mount` and Git `clone`.
- Both modes accept a caller-supplied local directory. The runtime does not require a Git repository or a clean Git worktree.
- `mount` uses the supplied directory without taking ownership of it. Container changes affect that directory.
- `copy` creates a pi-sesh-owned session copy. Container changes affect the copy, not the supplied source directory.
- Cleanup must never delete the supplied source or an externally owned checkout. It can delete the session copy only after container absence and copy ownership are proven.
- Reject obsolete `clone` configuration explicitly. Do not silently interpret it as `copy`.
- Workspace preparation returns paths, mode, and ownership/cleanup metadata. Repository URLs, refs, and source revisions do not belong in the Go runtime API.

### Mounted-path enforcement in Go

The mounted-workspace path now rejects broad host roots, the host home directory, known credential and container-engine state trees, and the host runtime directory. The manager also rejects workspace overlap with private session state. This is stricter than the TypeScript canonical-directory check and enforces the documented no-home/no-credentials/no-socket mount boundary. Ordinary project directories below home remain supported. These checks do not claim protection against a hostile process running as the same host user.

### Git and control-plane boundary

- go-sesh does not clone, fetch, select revisions, create branches, push, or create pull requests. It has no Git subprocess dependency for workspace preparation.
- A future sandboxd workspace provider can acquire a checkout and pass its local directory to the session runtime. The provider retains ownership of its source and repository metadata.
- Git providers must preserve the relevant clone hardening at that future boundary. They are not part of this Go rewrite.
- The orchestrator or integration owns branch/publish policy, push authorization, GitHub APIs, and pull requests.
- Workloads can still run Git inside the container through ordinary command execution. The session runtime does not interpret their Git semantics or automatically supply network access or credentials.

### Initial Go CLI

The Go executable is named `sesh`. The module remains `go-sesh`. Its default state directory is `$XDG_DATA_HOME/go-sesh`, or `$HOME/.local/share/go-sesh`, rather than TypeScript's pi-sesh directory. `--state-dir` explicitly selects another directory. Go does not import or migrate legacy records. Existing container names and ownership labels remain unchanged.

The first CLI supports start/inspect/list/stop/remove for detached mounted sessions. Start requires `--detach`, `--name`, an explicit local image, a directory, and literal argv after `--`. It does not load project configuration or provide attached/TTY, copy, resources, exec/logs, presets, or build behavior. Unsupported flags and commands fail rather than silently changing their meaning.

Positional session selectors are names; `--id UUID` explicitly selects immutable identity. Inspect always returns JSON, and other commands support `--json`. The CLI uses plain text for human summaries and lists; it does not port TypeScript's terminal color behavior. Flags precede positional operands. See the [CLI contract](cli.md) for syntax, JSON fields, and exit statuses.

### Go exec

Exec is implemented and unit-tested. Its CLI keeps the TypeScript requirement for both a name and matching `--session-id UUID`. The manager serializes the execution transaction with session lifecycle mutations. Unlike ordinary Stop, execution cancellation uses an immediate stop and accepts verified absence. Receipt storage, strict validation, atomic publication, and removal after verified container absence are implemented. The transaction saves a running receipt before execution and a final receipt after execution or recovery. Container cancellation has a separate 30-second timeout. Receipt persistence has its own 5-second timeout and still runs after caller cancellation or an expired container-stop budget. Live exec verification remains pending. See [execution support](exec.md).

### Decisions required before copy implementation

- Define symlink handling, special-file handling, size and entry limits, and copied permissions.
- Define behavior when the source changes during copying. The plan does not yet define an atomic snapshot guarantee.
- Define cancellation, partial-copy rollback, and ownership records that protect source directories and replaced paths.
- The initial Go store uses version 2 and rejects TypeScript version-1 records. Broader legacy inspection or cleanup still needs an explicit compatibility path. Never reinterpret an old clone record as a new copy record.
- Test copy behavior against its new filesystem contract. Use TypeScript for unchanged mount, resource, security, and lifecycle behavior.

The remainder of this document is the TypeScript baseline, not a specification to reintroduce Git provisioning into Go.

## Security

### Host process and Podman

- pi-sesh invokes Podman with an argv array. It does not build a shell command from project input.
- The workload command is literal argv. The command is passed as the container entrypoint and arguments. `exec` also passes literal argv after `--`.
- Podman is invoked with `--remote=false`. Preflight requires Linux x86-64, Podman 6.1.1 or later, a local rootless engine, cgroup v2, and memory and PID controllers.
- pi-sesh builds a restricted environment for its Podman process. It forwards only selected host variables, including `PATH`, `HOME`, XDG paths, locale values, and terminal values. It rejects remote-container settings and `CONTAINERS_*` overrides.
- pi-sesh filters non-absolute and unusable `PATH` entries. It also removes entries that resolve inside the selected project.
- `HOME` and the XDG variables used by the Podman process do not cause a host-home mount in the container.
- pi-sesh does not mount the host home directory or expose the Podman socket to the container.

### Container boundary

- A new session uses `--pull=never`. Starting a session does not pull an image. The image must already exist locally.
- The default runtime network is `none`. `pasta` is accepted only when explicitly selected and supported by the local rootless Podman engine.
- Project builds are a separate operation. A build uses `--network=pasta`; it does not inherit the runtime network default. Missing build base images are not pulled unless the caller explicitly requests `--pull` behavior.
- Containers use a read-only root filesystem, drop all capabilities, enable `no-new-privileges`, and set memory, CPU, and PID limits.
- The container runs with the invoking host UID and GID through `keep-id`. The workspace is writable at `/workspace`. `/home/session` and `/tmp` are writable tmpfs mounts with execution and device access disabled.
- The container receives a defined set of session variables and validated configuration variables. Host environment variables are not copied wholesale. `TERM` and `COLORTERM` are forwarded to the container only when a TTY is requested.

### Untrusted project input

- Project `pi-sesh.json` is untrusted. The loader validates a versioned schema, rejects unknown fields, limits file size, and validates command argv, image, network, workspace, and environment values.
- Project configuration can select a container image, a build context and recipe within the project, a workspace mode, a runtime network mode, an in-container command, and allowed in-container environment values.
- Project configuration cannot define a host preparation command, a host runtime mount, or a handoff manifest. Those inputs come from explicit user-level features.
- An in-container project command is data passed as argv. It is not a host command. A project Containerfile can run build instructions during an explicit image build; those instructions run as part of Podman's build and are not treated as host preparation commands.
- Configuration environment values are literal, single-line strings. Names that can override protected runtime behavior or commonly carry credentials are rejected. Entry count and value sizes are bounded.
- Build context and recipe paths must resolve within the selected project. The recipe must also be within the build context.
- Default project config symlinks must resolve inside the selected project. An explicitly selected config file can be outside it.

### Explicit user-level features

- Trusted launch presets are read from a user-owned, private file. They can select a host preparation argv, runtime program mounts, and a handoff manifest. Project configuration cannot set these fields.
- A preset preparation command is passed as literal argv, with a restricted environment and a private temporary directory. Its output is validated before use.
- Runtime mounts are explicit and read-only. Their sources must be files or directories, cannot be broad host roots or known credential trees, and cannot contain special files or foreign-owned entries. Symlinks must stay within the selected source tree.
- Handoff manifests must be explicitly selected or produced by a trusted preset. Their resources are copied into private session state before mounting. Handoff destinations are canonical paths below `/home/session/` and cannot overlap.

## Session lifecycle

Session state is stored below the user's XDG data directory, or below `~/.local/share` when `XDG_DATA_HOME` is unset. State directories are user-owned and set to mode `0700`. Session records and receipts use mode `0600`.

A session record binds a UUID and name to a container name, container ID, project path, workspace, immutable image ID, network mode, and creation time. Name reservations prevent two sessions from using the same name. Records are written atomically. The implementation validates records before use.

### Create and start

1. Validate the session name, effective settings, and literal command argv.
2. Preflight the local rootless Podman engine and verify that the selected image is already local. Resolve and record its immutable image ID.
3. Reserve the session name and prepare either a mounted project workspace or an isolated clone.
4. Stage any explicitly supplied resources and record their ownership data.
5. Create a container with pi-sesh ownership and session labels. The configured command is the container entrypoint.
6. Start the container. Attached mode attaches to the same container process. Detached mode starts it without an attached terminal.
7. Record and return the observed container state. A stopped session is not restarted by inspect, attach, exec, or logs.

If container creation or startup fails, pi-sesh checks ownership and container absence before deleting the session record, clone, or staged resources. If it cannot prove safe removal, it keeps the session reserved for recovery.

### Inspect

- Inspect can find a session by name or immutable session UUID.
- Before acting on a container, pi-sesh checks its ID and the `io.pi-sesh.owner` and `io.pi-sesh.session-id` labels against the session record. A mismatched or unverifiable container causes an error.
- Reported states are `running`, `exited`, `created`, `stopped`, `paused`, or `missing`. Unsupported Podman states fail inspection.
- Inspecting a stopped or missing session also triggers cleanup of its secret resources.
- Inspect does not load project configuration and does not restart the session.

### Exec

- Exec requires the name and matching immutable session UUID. It requires a verified running container.
- Exec accepts 1 to 256 literal argv values, without NUL bytes. Its working directory must be an absolute container path. The default is `/workspace`.
- An optional timeout must be greater than zero and no more than 86400 seconds.
- On cancellation or timeout, pi-sesh requests an immediate container stop and verifies that the container is stopped or missing. A lost exec client also triggers cancellation so that the workload is not intentionally left running.
- If cancellation cannot be verified, pi-sesh reports an error and directs the user to inspect and stop the session before further use.
- Exec writes a private receipt with start time, end time, result status when available, and outcome. Receipts remain until session removal.

### Stop

- Stop verifies the session identity and container ownership first.
- A running container is stopped with a 10-second grace period. A paused container is not stopped; pi-sesh asks the user to resume it first.
- A missing container is an error for `stop`; the user must remove the stale session explicitly.
- After stopping, pi-sesh verifies that the container is stopped or missing. It then removes secret resources.
- Stop retains the container, session record, clone workspace, and retained resources.

### Remove

- Remove refuses a running or paused container. It does not implicitly stop the session.
- If the container exists, pi-sesh requests removal and then verifies that it is absent. It verifies absence again before deleting session-owned data.
- After verified container absence, remove deletes staged handoff resources, the owned clone when applicable, execution receipts, the session record, and the name reservation.
- A missing container can be removed as a stale session, but resource cleanup still requires ownership validation.
- Any failed ownership or absence check stops cleanup. pi-sesh does not delete session data just because a Podman command failed.

### List

- List reads and validates each session record, then inspects its corresponding container.
- List does not restart sessions. As with inspect, listing a stopped or missing session triggers secret-resource cleanup.
- An invalid record or uncertain container identity is an error. The implementation does not silently treat invalid metadata as an unowned resource to delete.

### Logs and attach

- Logs requires a verified container. A missing container is an error. It reads the container's logs and does not restart it.
- Attach connects to the same running container process. It does not create or restart a container.
- Attach rejects a stopped or missing session. It also cleans secret resources when it observes a stopped or missing container.
- After logs or attach observes a stopped or missing container, it cleans secret resources.

## Workspace

### Mounted workspace

- `mount` resolves the selected project to a canonical existing directory and bind-mounts it read-write at `/workspace`.
- The project directory is the only project workspace mount. pi-sesh does not mount the host home directory.
- Changes made by the container are changes to the host project files.

### Clone workspace

- `clone` requires a clean Git worktree and a real in-tree `.git` directory. Linked worktrees, submodules, shared Git object stores, unsupported repository settings, symlinked Git attributes, and Git filter attributes are rejected.
- Clone setup runs Git with a controlled environment and literal argv. It disables hooks and other execution-affecting Git features, permits only the `file` protocol, creates an independent clone without hardlinks, and removes the clone's origin remote.
- The clone checks out the recorded source revision in detached mode. The resulting clone must have no remotes and must not use an alternate object store.
- The clone lives under private pi-sesh state and is writable at `/workspace`. Container changes do not change the source project.
- Clone cleanup requires a matching ownership record, session ID, expected path, device and inode identity, and a verified absent container. A replaced, symlinked, or foreign-owned path is not removed.

## Resources

Handoff resources are opt-in. The manifest supports snapshots and initially empty, writable owned directories. It allows at most 32 resources. Resource IDs and destinations must be unique, and destinations must not overlap.

### Snapshots

- A snapshot source is a relative path from the manifest directory. It must resolve inside that directory.
- Snapshot sources can contain only invoking-user-owned regular files and directories. Symlinks and special files are rejected.
- Source files and directories are checked for changes while staging. Staging copies data into private pi-sesh state; the container does not mount the original source path.
- Snapshots can be mounted read-only or read-write. A `secret` snapshot must be read-write and its source entries must have no group or other permissions.
- Staged payloads are bounded by file count, entry count, and total bytes.

### Writable owned directories

- An owned directory starts empty. It is mounted read-write below `/home/session/` and has retained lifetime.
- Its contents remain in private pi-sesh state after the container stops. Remove deletes them only after container absence and ownership checks succeed.

### Secret cleanup

- Secret resources are cleaned when startup or execution ends with a stopped or missing container, or when inspect, list, stop, attach, or logs observes a stopped or missing container.
- Secret cleanup removes only the secret resources. Retained snapshots and owned directories remain until session removal.
- If secret cleanup fails, pi-sesh keeps the resource in the session record for recovery and reports an error.
- Full handoff cleanup occurs during session removal or safe startup rollback.

### Ownership validation

- Staged resource records include a random token and device/inode identity. The staging directory also has an owner record that must match the session, resource ID, destination, access, lifetime, and identity.
- Cleanup checks private-directory ownership, file type, permissions, device/inode identity, and owner-record contents before removal.
- If those checks fail, pi-sesh leaves the resource in place and fails closed.

## Cleanup and failure handling

- Cleanup is fail-closed. A failed Podman call, invalid inspection result, mismatched ownership label, invalid session record, or uncertain container state does not prove that the container is absent.
- pi-sesh removes a container only after it verifies that the container belongs to the session. It removes session data only after it verifies container absence.
- pi-sesh does not delete a clone, handoff resource, execution receipt directory, or session record if ownership or container absence cannot be proven.
- Failed cleanup preserves the relevant record or name reservation when possible, so the session remains visible for recovery.
- Temporary preset preparation input is not mounted into the container. pi-sesh can remove that temporary input even when container status is uncertain.
- Bulk `clean` and `stop-all` preview by default. Applying them requires explicit confirmation. Paused sessions are skipped by `stop-all`.

## Deliberate boundaries and caveats

- `network: none` is the runtime default, not a restriction on explicitly requested `pasta` sessions or image builds.
- The host `HOME` value is available to the Podman host process for its own operation. It is not a container home mount or automatic container environment forwarding.
- The container root filesystem is read-only, but `/workspace`, `/home/session`, `/tmp`, and explicitly mounted writable handoff resources remain writable.
- The ownership checks reduce accidental or unsafe deletion. They do not claim protection from a hostile process running as the same host user that can alter that user's private state.
