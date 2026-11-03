# Go rewrite scope

This document classifies the target scope from the [updated rewrite plan](pi-sesh-go-rewrite-plan-updated.pdf). Targets are not implemented capabilities. `behavior.md` records the TypeScript baseline and the approved Go contract changes.

This repository, go-sesh, remains the Go replacement for pi-sesh. It is not a sandboxd or orchestrator implementation.

## Go v1

- A reusable session manager with no CLI or transport dependencies.
- A safe process runner for literal argv, explicit environments, bounded output, and cancellation.
- Versioned session records, name aliases, atomic writes, private permissions, and ownership validation.
- Local rootless Podman preflight and local image resolution.
- Start, open, exec, inspect, list, logs, stop, and remove.
- `mount` and filesystem `copy` workspaces from a caller-supplied local directory.
- Source preservation, session-copy ownership checks, and safe rollback and cleanup.
- Snapshot resources, retained writable owned directories, and conservative secret cleanup.
- Strict project configuration with the existing untrusted-input boundary and explicit rejection of obsolete `clone` mode.
- Explicit runtime network selection, with networking disabled by default.
- Cancellation handling and per-session locking for mutations.
- A thin CLI with machine-readable output for appropriate commands.
- Unit tests, fake-runtime lifecycle tests, TypeScript compatibility checks for unchanged behavior, copy-contract tests, and opt-in real Podman tests.

All unrelated security and cleanup invariants in `behavior.md` remain required. Replacing clone with copy must not weaken ownership validation or permit deletion of supplied source directories.

## Removed from the Go runtime

- Git `clone` workspace mode. It is removed, not an alias for `copy`.
- Host-side Git provisioning, fetch, revision selection, and checkout policy.
- Repository URLs, refs, source revisions, and Git-specific requests in the runtime API.

Git can still run inside a container through ordinary workload argv. That does not make the runtime responsible for Git provisioning, publishing, authentication, or repository semantics.

## Deferred until after the core release

- Trusted launch presets, host preparation commands, and runtime program mounts.
- Image build and default-image build commands.
- Attach, bulk clean, bulk stop, setup-prompt, and richer human output.
- A replacement for the TypeScript programmatic SDK. The Go v1 integration surface is the CLI process interface.

These convenience features are deferred, not removed. Do not add project-defined host preparation as a shortcut for missing presets. Until image-build commands exist, users must prepare images separately.

## Outside go-sesh responsibility

- sandboxd, scheduling, queues, leases, heartbeats, and supervision.
- Git workspace providers and checkout acquisition. A future sandboxd provider owns these operations and its checkout metadata.
- Branch/publish policy, push authorization, GitHub APIs, and pull requests. These belong to the orchestrator or integration.
- Cross-session admission control, accounting, and automatic garbage collection.
- Remote Podman connections or exposing the host Podman socket to sessions.
- Speculative support for another container runtime.

A future provider passes a local directory to go-sesh. For mount, it retains ownership of that directory until the session is safely removed. For copy, go-sesh owns only its session copy; the provider still owns the source. Provider implementation is deferred, not a new dependency to build here.

## Decisions and compatibility checks still required

- Define copy symlink and special-file handling, size and entry limits, permissions, source-mutation behavior, cancellation, rollback, and ownership metadata before implementation.
- Determine which TypeScript state records Go can safely read and remove. Never reinterpret a legacy clone record as a copy record.
- Preserve refusal to remove a running or paused session. The PDF's broad remove sequence is not permission to add implicit stop behavior.
- Preserve recovery metadata before container creation. The PDF's simplified start sequence does not replace the TypeScript recovery ordering.
- Compare unchanged behavior with TypeScript. Test copy semantics separately against the agreed filesystem contract.
- Resolve any remaining difference between the plan and observed behavior explicitly before implementation.

## Implementation progress and next slice

The captured-output host process runner is implemented and tested. It does not provision Git workspaces or run container lifecycle operations.

Session ID parsing and validation, neutral runtime state validation, and stopped/missing classification are implemented and tested. These checks do not establish container ownership or replace live runtime inspection.

Workspace and network mode validation are implemented and tested, including explicit rejection of obsolete clone mode and unsupported network modes. Mounted-directory preparation is implemented. Copy preparation remains pending.

The first file-backed store slice creates name reservations and version-2 records, then loads records by ID or name after reopening. It validates private files, rejects legacy/unknown schemas, prevents identity overwrites, and serializes metadata writes. Name-based resolution checks reservation/record agreement and preserves lookup errors. Atomic updates can bind the pending container ID while preserving other saved facts and the name reservation. Listing returns validated saved records and rejects partial results on errors. Matching mount-session metadata removal and per-session lifecycle locks are implemented. Resource/copy ownership metadata remains pending. See [session store notes](session-store.md).

The first runtime boundary has neutral inspection types, a consumer-owned Inspector interface, and a Podman adapter with restricted argv/environment, rootless preflight, and verified inspection. The manager's Inspect/List methods are implemented and tested with fake inspectors; they preserve errors and keep missing sessions reserved. The adapter's ImageExists method is implemented and tested. It reports local image presence without pulling. See [runtime inspection](runtime-inspection.md).

Immutable image resolution is implemented by `Client.ImageID` and `parseImageID`. Tests cover both output formats, malformed IDs, command failures, cancellation, and failed preflight. Container-only `Client.Stop` and `Client.Remove` are implemented and tested with fake runners. Both pin the observed container ID. Stop verifies the final stopped/exited/missing state. Remove refuses running/paused containers and verifies absence before success. Creation-request data and pure validation are implemented. Custom environment validation follows the TypeScript protected-name and byte-limit rules. Secure create argv construction is implemented through small argument-section helpers. Container-ID output parsing is also implemented and tested. See the [container creation boundary](container-creation.md).

The manager's detached mount workflow now implements Start/Inspect/Stop/Remove with per-session locks, pending recovery records before create, container-ID binding before start, and bounded verified startup rollback. Fake-runtime tests exercise the full flow and failure paths. The Podman Create and StartDetached methods are implemented and tested with fake process runners. The complete adapter contract is checked at compile time. The approved opt-in live mounted-session test passed on rootless Podman 6.1.4 with `localhost/go-sesh-smoke:1`. It verified workspace writes, stop/removal, source preservation, and test-state cleanup. See the [mounted-session lifecycle](mounted-session.md).

The minimal `sesh` CLI now exposes start/inspect/list/stop/remove for detached mounted sessions, with JSON output, explicit literal argv, and a separate default `go-sesh` state directory. The built CLI passed a live smoke workflow with source preservation and unchanged existing session IDs. The module, record schema, and ownership labels remain unchanged. See the [CLI contract](cli.md).

Next work extends this usable path toward exec/logs, configuration, copy workspaces, and resources. These remain separate features, not completed CLI parity. Keep Git provisioning and orchestration outside these types and APIs.
