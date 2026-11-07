# sesh CLI

## Current scope

`sesh` exposes the verified detached mounted-session path. It supports start, inspect, list, stop, and remove. It uses the standard library `flag` package and has no CLI framework dependency.

Exec supports literal argv in a verified running session, live output, optional timeouts, workload exit statuses, and private execution receipts. Cancellation stops the container and verifies the result. Unit tests cover the transaction; live exec verification remains pending. See [execution support](exec.md).

The CLI does not implement Podman policy or cleanup. It does not load project configuration. Unsupported commands and flags fail explicitly.

## Command contract

```text
sesh [--state-dir DIR] [--json] COMMAND [flags] [arguments]

start --detach --name NAME --image IMAGE [--network none|pasta]
      [--workspace mount] [--env NAME=VALUE ...] DIRECTORY -- PROGRAM [ARG ...]
inspect NAME | --id UUID
list
stop NAME | --id UUID
remove NAME | --id UUID
exec --session-id UUID [--cwd /workspace] [--timeout SECONDS] NAME -- PROGRAM [ARG ...]
```

Flags must precede positional arguments. Common flags also work after the command, before its operands. Workload argv must follow `--`; it is never re-parsed as CLI flags. Literal spaces, shell characters, and values beginning with `--` remain separate workload arguments.

Start requires explicit detached mode because attached operation is not implemented. It requires an image rather than reading project defaults or building an image. Mount is the only operational workspace mode. Copy and obsolete clone mode are refused. Network defaults to none.

Names and IDs are not guessed. A positional UUID-shaped string is still a name. Only `--id UUID` selects immutable identity. Name lookup resolves once to a saved UUID before the manager operates; alias reuse cannot redirect an operation to a different ID.

Repeated `--env` flags split at the first `=`. Empty values and additional `=` characters are preserved. Empty names, missing separators, and duplicate names fail parsing. The runtime owns the full protected-name and byte-limit policy. Host environment is not automatically forwarded.

Help, unknown commands, and invalid CLI syntax do not open Podman or state storage. Help works even when Podman is absent from PATH.

## State and compatibility

The binary is `sesh`, while the module remains `go-sesh`. Default state is `$XDG_DATA_HOME/go-sesh`, falling back to `$HOME/.local/share/go-sesh`. These environment paths must be absolute. An explicit `--state-dir` may be relative to the working directory.

This intentionally differs from TypeScript's `pi-sesh` state namespace. Existing TypeScript sessions are not imported or migrated. Ownership labels, container-name prefixes, and the version-2 record format remain unchanged.

The first CLI is a supported subset, not complete TypeScript CLI parity. It adds explicit JSON output and state selection. It omits project defaults, config files, resources, copy provisioning, TTY, attachment, logs, presets, and build commands.

## Output and status

- Inspect always writes one JSON object.
- Start and Stop write a human summary, or one JSON object with `--json`.
- List writes a plain table, or a JSON array. An empty JSON list is `[]`, not `null`.
- Remove writes a confirmation, or `{ "id": "...", "removed": true }`.
- Diagnostics go to stderr. Errors are printed once.
- JSON uses a CLI-specific struct, not automatic serialization of internal domain types.
- `containerId` is the saved identity. `observedContainerId` is included only when observed. Pending recovery records therefore do not silently change stored facts.
- `exitCode` is omitted when unavailable. A known zero exit code remains present.
- Output does not contain the container environment or resource secrets.
- Text output quotes paths to prevent embedded tabs/newlines from changing row structure. It has no TTY color behavior yet.

Exit statuses are 0 for success/help, 2 for usage errors, 130 for context cancellation, and 1 for other failures. Exec preserves ordinary workload exit statuses without adding a diagnostic. SIGINT and SIGTERM both cancel the manager context. Startup rollback can finish with its independent bounded cleanup context before the CLI exits. This initial CLI uses 130 for either cancellation signal.

The CLI borrows stdout/stderr and never closes them. It closes its opened store even after command or output failures. If detached startup succeeded but output fails, the CLI reports an error and leaves the saved session available for inspection; it does not silently stop a successful detached workload.

## Control flow

```text
cmd/sesh.main
  run -> signal.NotifyContext(SIGINT, SIGTERM)
  select process environment and writers
  cli.Run(args, Options)
    parse root/common flags and command flags
    help -> write help; no backend
    usage error -> stderr and status 2; no backend
    resolve cwd and state directory
    Open(stateDir, projectDir)
      podman.New(filtered host environment, project PATH exclusion)
      session.OpenStore(private state directory)
      backend embeds Manager and adds name lookup / store Close
    dispatch
      start -> Manager.Start
      inspect -> resolve name or explicit ID -> Manager.Inspect
      list -> Manager.List
      stop -> resolve name or explicit ID -> Manager.Stop
      remove -> resolve name or explicit ID -> Manager.Remove
    render text or typed JSON through io.Writer
    close backend; preserve command and close errors
    map error to process status
```

Sources:

- [cmd/sesh/main.go](../cmd/sesh/main.go): process setup and concrete dependency construction.
- [internal/cli/parse.go](../internal/cli/parse.go): flags, literal argv, environment entries, and default state selection.
- [internal/cli/run.go](../internal/cli/run.go): dispatch, backend lifetime, error output, and statuses.
- [internal/cli/output.go](../internal/cli/output.go): JSON schema and borrowed-writer output.

## Verification

Unit tests exercise parsing, help without Podman, backend-free usage errors, name/ID selection, literal argv and environment values, JSON, state isolation, backend closure, output failures, and exit statuses.

The built binary passed an approved live workflow with `localhost/go-sesh-smoke:1`: start, inspect by name, JSON list, refusal to remove a running session, stop/remove by UUID, and an empty final list. Literal environment text containing `=`, `$HOME`, and shell syntax arrived unchanged. Workspace output remained after removal. Container absence was verified before test files were removed, and existing session container IDs were unchanged.
