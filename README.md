# sesh

A Go CLI for local rootless Podman sessions. This repository, `go-sesh`, rebuilds pi-sesh with a reusable session manager and a thin CLI.

The first working path supports detached sessions with a mounted directory: **start → inspect → list → stop → remove**. It has passed live tests through both the manager and the built CLI.

## Build

Use Go 1.26 or later. The runtime requires Linux x86-64, local rootless Podman 6.1.1 or later, and cgroup v2 with memory and PID controllers.

```sh
go build -o bin/sesh ./cmd/sesh
./bin/sesh --help
```

To install the executable into your Go binary directory:

```sh
go install ./cmd/sesh
```

The executable is `sesh`; the module and default state directory remain `go-sesh`. A tmux session manager also uses the command name `sesh`, so check for a PATH collision if you use that tool.

## Use

Select an image that already exists locally. The following example uses the dedicated smoke image prepared on this machine:

```sh
./bin/sesh start --detach --name demo \
  --image localhost/go-sesh-smoke:1 . -- /bin/sleep 300

./bin/sesh inspect demo
./bin/sesh list
./bin/sesh stop demo
./bin/sesh remove demo
```

Put flags before the workspace directory or session name. Put workload argv after `--`. Arguments stay literal; sesh does not construct a host shell command.

The workspace is mounted read-write. Container changes affect its files. Stop retains the session and workspace. Remove refuses running or paused containers and never deletes the supplied workspace directory.

Networking defaults to `none`. Use `--network pasta` explicitly when needed. Images are never pulled during session start. The container root filesystem is read-only, capabilities are dropped, and resource limits remain enforced.

### Output and identity

`inspect` always writes JSON. Use `--json` for other commands:

```sh
./bin/sesh list --json
./bin/sesh stop --json demo
./bin/sesh remove --id SESSION_UUID --json
```

Positional selectors are names, even when they look like UUIDs. `--id UUID` explicitly selects immutable identity. Sesh does not guess between names and IDs.

### Container environment

Use repeated `--env NAME=VALUE` flags before the directory. Values remain literal and may contain `=`. Duplicate names are refused. The runtime rejects protected and credential-like variables. It does not copy the host environment into the container.

### State

Default state is `$XDG_DATA_HOME/go-sesh`, or `$HOME/.local/share/go-sesh` when XDG data home is unset. Use `--state-dir DIR` to select another private directory:

```sh
./bin/sesh --state-dir /absolute/private/state list --json
```

Global flags also work after the command, before its operands. Relative explicit state directories resolve against the working directory. Existing unsafe ownership, permissions, symlinks, or records are refused rather than repaired.

TypeScript pi-sesh state remains separate. Go rejects version-1 records and does not migrate them. Container names and ownership labels retain the existing pi-sesh values for compatibility.

## Current limits

- Start requires `--detach`, `--name`, an explicit local `--image`, a directory, and workload argv.
- Only mounted workspaces are operational. Copy preparation is not implemented; obsolete clone mode is rejected.
- Project configuration is not loaded. `--config`, TTY, attachment, logs, resources, presets, and image builds are not implemented by the CLI.
- Exec supports literal argv, live output, optional timeouts, private receipts, and verified container cancellation. Supply both the name and matching `--session-id UUID`. Exec is unit-tested but has not yet passed a live Podman test. See [execution support](docs/exec.md).
- Failed or cancelled startup uses verified rollback. Uncertain cleanup preserves state for recovery.
- A detached workload can finish before start returns. A successful start reports the observed state, not a promise that it remains running.

## Development

```sh
go test ./...
go vet ./...
go test -race ./...
```

Ordinary tests do not require Podman. Live tests require explicit opt-in and a prepared local image. See [live verification](docs/mounted-session.md#verification) before running them.

## References

- [CLI contract and control flow](docs/cli.md)
- [Mounted-session lifecycle](docs/mounted-session.md)
- [Podman control-flow guide](docs/podman-control-flow.md)
- [Session store](docs/session-store.md)
- [Container creation boundary](docs/container-creation.md)
- [Behavior baseline and approved Go changes](docs/behavior.md)
- [Go release scope](docs/scope.md)
- TypeScript reference: `../pi-sesh/`
