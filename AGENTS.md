# AGENTS.md — go-sesh, the pi-sesh Go rebuild

## Project context

This repository is rebuilding `pi-sesh` in Go. The current TypeScript implementation is in `../pi-sesh/` and is the behavioral reference.

Read `docs/pi-sesh-go-rewrite-plan-updated.pdf`, `docs/behavior.md`, and `docs/scope.md` before architectural or behavioral changes. The updated plan defines the target architecture. The behavior document separates the TypeScript baseline from approved Go contract changes. The scope document classifies Go v1, deferred features, and removed behavior. Check TypeScript for unchanged behavior when needed. Do not assume that a planned behavior already exists.

The goal is a small, readable, idiomatic Go CLI for rootless Podman sessions. It runs literal argv in a container and accepts a supplied local directory through `mount` or filesystem `copy`. Git clone mode is intentionally removed. Git workspace provisioning and orchestration are outside go-sesh.

## Engineering priorities

In order of importance:

1. Preserve the documented security and ownership invariants.
2. Provide correct, predictable session behavior.
3. Write readable, idiomatic Go.
4. Keep behavior testable with explicit dependencies.
5. Prefer simple designs over unnecessary abstractions.
6. Optimize only when measurements justify it.

**Write Go, not TypeScript translated into Go.** Keep the behavior contract, but use Go's types, errors, and control flow naturally.

## Go conventions

Use these references when making style decisions:

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- [Google Go Style Guide](https://google.github.io/styleguide/go/)
- [Go Proverbs](https://go-proverbs.github.io/)

### Naming and organization

- Use short, descriptive, lowercase package names.
- Avoid package names such as `utils`, `common`, `helpers`, or `base`.
- Avoid stuttering names such as `container.ContainerConfig` when `container.Config` is clear.
- Use MixedCaps or mixedCaps for identifiers, not underscores.
- Keep exported identifiers to the minimum required by package boundaries.
- Document exported types and functions with comments that begin with their names.
- Prefer names that communicate intent.
- Keep functions focused. Do not split simple logic into unnecessary helpers.
- Prefer early returns over deeply nested conditionals.
- Avoid named return values unless they improve clarity.

### Types and interfaces

- Prefer concrete types by default.
- Define interfaces where they are consumed, when multiple implementations or a useful testing boundary justify them.
- Keep interfaces small and focused on behavior.
- Do not create interfaces just to mock every concrete dependency.
- Prefer typed structs for known data contracts over `map[string]any`.
- Avoid `any` when the data has a known shape.
- Do not add generics for a one-off operation.
- Before adding an abstraction, ask whether direct code would be easier to understand and maintain.

### Errors and resources

- Handle errors explicitly. Return errors for expected failures instead of panicking.
- Wrap errors with useful context using `fmt.Errorf` and `%w`.
- Do not wrap an error repeatedly with redundant context.
- Use `errors.Is` and `errors.As` when callers need to identify an error category.
- Use sentinel or typed errors only when callers need to act on the category.
- Do not silently discard errors or use logging as a substitute for returning them.
- Do not log and return the same error at the same layer.
- Close files and process pipes. Use `defer` when it makes resource ownership clear.
- Pass `context.Context` to operations that can block or be cancelled. Do not store contexts in structs unless an API requires it.
- Avoid global mutable state. Pass dependencies through constructors or function arguments where useful.

### Concurrency

Concurrency is not a default requirement.

- Prefer synchronous control flow unless concurrency has a clear benefit.
- Avoid goroutines and channels for work that can use ordinary process or command APIs.
- Avoid shared mutable state between goroutines.
- Do not add mutexes without a real synchronization requirement.
- Keep cancellation and cleanup paths clear. A cancelled caller must not leave a workload running without an explicit, verified policy.

## Architecture

Keep process setup, CLI behavior, and system operations separate. Use the smallest package structure that makes these boundaries clear. A likely starting point is `cmd/pi-sesh` for startup and `internal/` packages for configuration, Podman, session lifecycle, workspaces, and handoff resources. Do not create every package in advance or add layers without a concrete need.

- Process startup owns argument parsing, environment selection, dependency construction, and exit status. Keep `main` small.
- CLI code owns command dispatch and user-facing output. It should not implement Podman policy or resource cleanup itself.
- Podman code owns literal process invocation, local rootless preflight, image inspection, and container operations.
- Session code owns session records, identity checks, lifecycle ordering, and rollback. Persisted records do not establish live container state.
- Runtime code defines neutral container operations and state types. Keep Podman flags and JSON in the Podman package.
- Workspace code owns filesystem mount/copy preparation and safe cleanup of its own copies. It does not own supplied sources or Git provisioning.
- Resource code owns handoff validation, staging, ownership records, and safe removal.
- Keep package dependencies directed toward the operations they use. Avoid cycles and avoid exposing internal types without need.

Do not port TypeScript module boundaries mechanically. Choose Go package boundaries based on cohesive responsibilities and the actual callers.

### Directory and Git boundary

- The runtime accepts local directories, not repository URLs, refs, or source revisions.
- Use `workspace/manager.go`, `mount.go`, and `copy.go` when those components are implemented. Do not add clone or Git subprocess modules.
- Reject obsolete `clone` configuration explicitly. Never silently treat it as `copy`.
- A future sandboxd workspace provider owns checkout acquisition, repository metadata, and provider cleanup. Do not implement that provider here.
- The orchestrator or integration owns branch/publish policy, push authorization, GitHub APIs, and pull requests.
- Workloads can run Git inside the container as ordinary commands. The runtime must not interpret their repository semantics.

## Container and security invariants

Preserve the unchanged guarantees and approved Go changes in `docs/behavior.md`. In particular:

- Invoke Podman and workload commands with argv. Never construct a shell command from project-controlled strings.
- Use local rootless Podman. Do not enable remote Podman or expose a Podman socket to the workload.
- Treat project configuration as untrusted. It must not select arbitrary host commands or host mounts.
- Keep host environment forwarding explicit. Do not copy the host environment into a container.
- Do not mount the host home directory or credential stores into a workload.
- Do not pull images automatically when starting sessions. Require the image to exist locally.
- Disable runtime networking by default. Keep image-build networking behavior distinct from session networking.
- Keep the container root filesystem read-only, drop capabilities, and preserve resource limits and `no-new-privileges` behavior.
- Keep workspace and handoff mounts explicit and validate their paths and access modes.
- Mount does not transfer source ownership. Copy owns only the session copy. Cleanup must never delete a supplied source or a provider-owned checkout.
- Permit host preparation commands and runtime program mounts only through the explicitly selected, private user preset path described by the TypeScript contract.
- Verify container ownership before operating on a container. Verify absence before deleting session-owned data.
- Fail closed. Do not delete resources when ownership, identity, or container absence cannot be proven.

A host path and a container path are different namespaces. Validate each with the correct rules. Do not assume a path inside the container has the same meaning on the host.

## Behavior and compatibility

- Read `docs/behavior.md` before implementing or changing session behavior.
- Inspect the matching code and tests in `../pi-sesh/src/` to confirm edge cases and failure ordering.
- Preserve unchanged TypeScript behavior. Apply the approved replacement of Git clone with filesystem copy. Keep planned behavior separate from implemented capabilities in summaries and documentation.
- Keep unrelated CLI command names and observable behavior consistent with the reference.
- Define copy symlink handling, limits, permissions, cancellation, source-mutation behavior, and rollback before implementation. Do not invent those policies during the port.
- Do not reinterpret legacy clone records as copy records. Establish safe state compatibility explicitly.
- Do not silently weaken ownership checks, cleanup guarantees, argv handling, network defaults, or image-pull behavior.
- Treat a failed Podman operation or an invalid inspection response as uncertainty, not proof that a container is absent.
- Keep compatibility decisions explicit. Do not add speculative features or silently remove existing behavior.

## Testing

Use Go's standard `testing` package by default. Prefer table-driven tests when they make related cases clear. Test observable behavior, including failure cases, not only private helpers.

- Test command construction as argv slices. Include arguments with spaces and shell metacharacters to ensure they remain literal.
- Use fake process runners or narrow interfaces at real system boundaries. Do not require Podman for ordinary unit tests.
- Use temporary directories to test file ownership metadata, path validation, staging, and cleanup.
- Test ownership mismatches, symlinks, invalid records, interrupted operations, and uncertain container states.
- Test source preservation for both mount and copy. Test copy ownership, cancellation, and rollback against the new contract, not against TypeScript clone behavior.
- Keep Podman integration tests opt-in. Do not run tests that change host credentials or require a live provider without user approval.
- Keep tests deterministic. Avoid sleeps and timing-sensitive assertions where possible.
- Add regression tests when fixing a bug.

When Go code exists, run relevant tests and checks before completion. At minimum, use:

```sh
gofmt -w <changed Go files>
go test ./...
go vet ./...
```

Use `go test -race ./...` when a change has concurrency or shared mutable state. Do not report checks as passed unless they ran successfully. If a check cannot run, state why.

## Dependencies

Prefer the Go standard library when it gives a clear solution. Before adding a dependency:

1. Check whether the standard library or an existing dependency is sufficient.
2. Consider maintenance, API stability, security, and dependency size.
3. Add the dependency only when it materially improves the design.

Do not add a framework to save a small amount of ordinary Go code.

## Comments and documentation

- Write comments to explain why non-obvious behavior exists. Do not restate the code.
- Document exported APIs and important security, ownership, and concurrency decisions.
- Keep `README.md` focused on installation, configuration, and use.
- Keep the behavioral baseline in `docs/behavior.md`.
- Do not document planned behavior as if it already exists.
- Use short, clear sentences and active voice in documentation.

## Scope and change discipline

- Implement the smallest coherent change that meets the request.
- Inspect relevant code and tests before making changes.
- Keep changes focused. Do not refactor unrelated code.
- Add or update tests with behavior changes.
- Review the diff for unnecessary complexity.
- Ask for clarification when an ambiguity changes a security boundary or an observable contract.

## Go and container learning

The maintainer wants to learn Go and container behavior while building this project.

- Explain non-obvious Go design choices when they are relevant. Highlight useful concepts such as interfaces at system boundaries, error wrapping, contexts, and process execution.
- Explain container behavior when it affects a design choice. Distinguish host processes from container processes, host paths from container paths, and image-build behavior from runtime behavior.
- When a Go or container concept differs from a likely TypeScript or general application assumption, explain the difference briefly.
- Teach through clear code and concise explanations. Do not add long comments or explain elementary programming concepts unless asked.
- Do not sacrifice idiomatic Go or security to make the code look familiar from TypeScript.

**The goal is a small, readable Go session runtime that preserves pi-sesh safety, applies the approved directory-workspace contract, and keeps Git provisioning and orchestration outside its boundary.**
