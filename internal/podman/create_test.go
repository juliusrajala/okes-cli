package podman

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

func TestCreateIdentityArgs(t *testing.T) {
	request := testCreateRequest()
	request.Name = "another-session"
	request.Target.SessionID = strings.ToUpper(request.Target.SessionID)
	request.Target.ContainerName = "pi-sesh-" + request.Target.SessionID
	got := createIdentityArgs(request)
	want := []string{
		"create",
		"--name", request.Target.ContainerName,
		"--label", "io.pi-sesh.owner=pi-sesh",
		"--label", "io.pi-sesh.session-id=" + request.Target.SessionID,
		"--label", "io.pi-sesh.session-name=another-session",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("identity argv = %q, want %q", got, want)
	}
	got[0] = "changed"
	if next := createIdentityArgs(request); next[0] != "create" {
		t.Fatal("identity results share mutable storage")
	}
}

func TestCreatePolicyArgs(t *testing.T) {
	for _, network := range []runtime.NetworkMode{runtime.NetworkNone, runtime.NetworkPasta} {
		t.Run(string(network), func(t *testing.T) {
			host := createHost{UID: 2000, GID: 3000}
			got := createPolicyArgs(network, host)
			want := []string{
				"--pull=never",
				"--image-volume=ignore",
				"--http-proxy=false",
				"--network", string(network),
				"--userns=keep-id",
				"--user", "2000:3000",
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
			if !slices.Equal(got, want) {
				t.Fatalf("policy argv = %q, want %q", got, want)
			}
			got[0] = "--pull=always"
			if next := createPolicyArgs(network, host); next[0] != "--pull=never" {
				t.Fatal("policy results share mutable storage")
			}
		})
	}
}

func TestCreateEnvironmentArgs(t *testing.T) {
	request := testCreateRequest()
	request.Name = "another-session"
	request.Network = runtime.NetworkPasta
	request.WorkspaceMode = workspace.Copy
	before := request
	before.Command = slices.Clone(request.Command)
	before.Environment = maps.Clone(request.Environment)
	entries := []string{"A_VALUE=literal=$HOME; $(whoami)", "Z_VALUE="}
	got := createEnvironmentArgs(request, entries)
	want := []string{
		"--env", "HOME=/home/session",
		"--env", "XDG_CONFIG_HOME=/home/session/.config",
		"--env", "XDG_DATA_HOME=/home/session/.local/share",
		"--env", "XDG_CACHE_HOME=/home/session/.cache",
		"--env", "LANG=C.UTF-8",
		"--env", "PI_SESH_SESSION=another-session",
		"--env", "PI_SESH_SESSION_ID=" + request.Target.SessionID,
		"--env", "PI_SESH_NETWORK=pasta",
		"--env", "PI_SESH_WORKSPACE=copy",
		"--env", "A_VALUE=literal=$HOME; $(whoami)",
		"--env", "Z_VALUE=",
	}
	if !slices.Equal(got, want) || !reflect.DeepEqual(request, before) {
		t.Fatalf("environment argv = %q, want %q; input must stay unchanged", got, want)
	}
	got[len(got)-1] = "changed"
	if entries[1] != "Z_VALUE=" {
		t.Fatal("environment result shares mutable storage with entries")
	}
	if next := createEnvironmentArgs(request, nil); !slices.Equal(next, want[:18]) {
		t.Fatalf("fixed environment = %q, want %q", next, want[:18])
	}
}

func TestCreateTerminalArgs(t *testing.T) {
	for _, tt := range []struct {
		name        string
		tty         bool
		interactive bool
		term        string
		colorTerm   string
		want        []string
	}{
		{name: "no terminal forwarding", term: "not-forwarded", colorTerm: "not-forwarded"},
		{name: "stdin only", interactive: true, term: "not-forwarded", want: []string{"--interactive"}},
		{name: "TTY implies stdin", tty: true, term: "xterm-256color", colorTerm: "truecolor", want: []string{"--interactive", "--tty", "--env", "TERM=xterm-256color", "--env", "COLORTERM=truecolor"}},
		{name: "TTY and explicit stdin", tty: true, interactive: true, want: []string{"--interactive", "--tty"}},
		{name: "only TERM", tty: true, term: "xterm;$(whoami)", want: []string{"--interactive", "--tty", "--env", "TERM=xterm;$(whoami)"}},
		{name: "only COLORTERM", tty: true, colorTerm: "truecolor", want: []string{"--interactive", "--tty", "--env", "COLORTERM=truecolor"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := testCreateRequest()
			request.TTY, request.Interactive = tt.tty, tt.interactive
			host := testCreateHost()
			host.Term, host.ColorTerm = tt.term, tt.colorTerm
			if got := createTerminalArgs(request, host); !slices.Equal(got, tt.want) {
				t.Fatalf("terminal argv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCreateArgs(t *testing.T) {
	request := testCreateRequest()
	host := testCreateHost()
	before := request
	before.Command = slices.Clone(request.Command)
	before.Environment = maps.Clone(request.Environment)
	got, err := buildCreateArgs(request, host)
	if err != nil {
		t.Fatal(err)
	}
	// This is the TypeScript create policy, with directory workspace modes.
	// The host Podman invocation adds --remote=false outside this builder.
	want := []string{
		"create",
		"--name", request.Target.ContainerName,
		"--label", "io.pi-sesh.owner=pi-sesh",
		"--label", "io.pi-sesh.session-id=" + request.Target.SessionID,
		"--label", "io.pi-sesh.session-name=checks",
		"--pull=never",
		"--image-volume=ignore",
		"--http-proxy=false",
		"--network", "none",
		"--userns=keep-id",
		"--user", "1000:1001",
		"--read-only",
		"--cap-drop=all",
		"--security-opt=no-new-privileges",
		"--memory=4g",
		"--memory-swap=4g",
		"--cpus=4",
		"--pids-limit=512",
		"--tmpfs", "/home/session:rw,noexec,nosuid,nodev,size=1g,mode=1777",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=512m,mode=1777",
		"--volume", "/work/project:/workspace:rw",
		"--workdir", "/workspace",
		"--env", "HOME=/home/session",
		"--env", "XDG_CONFIG_HOME=/home/session/.config",
		"--env", "XDG_DATA_HOME=/home/session/.local/share",
		"--env", "XDG_CACHE_HOME=/home/session/.cache",
		"--env", "LANG=C.UTF-8",
		"--env", "PI_SESH_SESSION=checks",
		"--env", "PI_SESH_SESSION_ID=" + request.Target.SessionID,
		"--env", "PI_SESH_NETWORK=none",
		"--env", "PI_SESH_WORKSPACE=mount",
		"--env", "NODE_ENV=test",
		"--env", "PROJECT_PROFILE=local",
		"--entrypoint", "worker", request.ImageID,
		"--literal", "argument with spaces", "$(touch /tmp/not-executed)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("create argv = %q\nwant        = %q", got, want)
	}
	if !reflect.DeepEqual(request, before) || host != testCreateHost() {
		t.Fatal("builder modified an input")
	}

	t.Run("literal workload argv", func(t *testing.T) {
		request := testCreateRequest()
		request.Command = []string{"program with spaces", "--privileged", "x;y", "$(whoami)", "'$HOME'", "a\nb"}
		got, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		want := append([]string{"--entrypoint", request.Command[0], request.ImageID}, request.Command[1:]...)
		if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
			t.Fatalf("workload argv was changed: %q, want suffix %q", got, want)
		}
	})

	t.Run("literal workspace path with SELinux", func(t *testing.T) {
		request := testCreateRequest()
		request.WorkspacePath = "/work/a b;$HOME"
		host := testCreateHost()
		host.SELinuxEnabled = true
		got, err := buildCreateArgs(request, host)
		if err != nil {
			t.Fatal(err)
		}
		index := slices.Index(got, "--volume")
		if index < 0 || index+1 >= len(got) || got[index+1] != "/work/a b;$HOME:/workspace:rw,Z" {
			t.Fatalf("invalid explicit workspace volume: %q", got)
		}
	})

	t.Run("explicit pasta and prepared copy mode", func(t *testing.T) {
		request := testCreateRequest()
		request.Network = runtime.NetworkPasta
		request.WorkspaceMode = workspace.Copy
		got, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		index := slices.Index(got, "--network")
		if index < 0 || index+1 >= len(got) || got[index+1] != "pasta" ||
			!slices.Contains(got, "PI_SESH_NETWORK=pasta") || !slices.Contains(got, "PI_SESH_WORKSPACE=copy") {
			t.Fatalf("network or workspace mode was changed: %q", got)
		}
	})

	t.Run("one entrypoint and no custom environment", func(t *testing.T) {
		request := testCreateRequest()
		request.Command = []string{"worker"}
		request.Environment = nil
		got, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got[len(got)-3:], []string{"--entrypoint", "worker", request.ImageID}) || slices.Contains(got, "NODE_ENV=test") {
			t.Fatalf("invalid single-command argv: %q", got)
		}
	})

	for _, tt := range []struct {
		name        string
		tty         bool
		interactive bool
	}{
		{name: "detached without stdin"},
		{name: "stdin without TTY", interactive: true},
		{name: "TTY implies stdin", tty: true},
		{name: "TTY and explicit stdin", tty: true, interactive: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := testCreateRequest()
			request.TTY, request.Interactive = tt.tty, tt.interactive
			host := testCreateHost()
			host.Term, host.ColorTerm = "xterm-256color", "truecolor"
			got, err := buildCreateArgs(request, host)
			if err != nil {
				t.Fatal(err)
			}
			interactiveCount, ttyCount := 0, 0
			for _, arg := range got {
				if arg == "--interactive" {
					interactiveCount++
				}
				if arg == "--tty" {
					ttyCount++
				}
			}
			wantInteractive, wantTTY := 0, 0
			if tt.tty || tt.interactive {
				wantInteractive = 1
			}
			if tt.tty {
				wantTTY = 1
			}
			if interactiveCount != wantInteractive || ttyCount != wantTTY ||
				slices.Contains(got, "TERM=xterm-256color") != tt.tty ||
				slices.Contains(got, "COLORTERM=truecolor") != tt.tty {
				t.Fatalf("unexpected terminal flags/environment: %q", got)
			}
		})
	}

	t.Run("TTY does not synthesize missing terminal values", func(t *testing.T) {
		request := testCreateRequest()
		request.TTY = true
		got, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range got {
			if strings.HasPrefix(arg, "TERM=") || strings.HasPrefix(arg, "COLORTERM=") {
				t.Fatalf("builder synthesized terminal environment: %q", got)
			}
		}
	})

	t.Run("custom environment is sorted and literal", func(t *testing.T) {
		request := testCreateRequest()
		request.Environment = map[string]string{"Z_VALUE": "=$HOME; $(whoami)", "A_VALUE": ""}
		got, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		a, z := slices.Index(got, "A_VALUE="), slices.Index(got, "Z_VALUE==$HOME; $(whoami)")
		if a < 1 || z <= a || got[a-1] != "--env" || got[z-1] != "--env" {
			t.Fatalf("custom environment was changed or not sorted: %q", got)
		}
	})

	for _, tt := range []struct {
		name   string
		change func(*runtime.CreateRequest, *createHost)
	}{
		{name: "invalid identity", change: func(r *runtime.CreateRequest, _ *createHost) { r.Target.ContainerName = "unrelated" }},
		{name: "existing container", change: func(r *runtime.CreateRequest, _ *createHost) { r.Target.ContainerID = strings.Repeat("b", 64) }},
		{name: "tag instead of immutable ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = "localhost/worker:1" }},
		{name: "unsafe volume syntax", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "/work/project:ro" }},
		{name: "host network", change: func(r *runtime.CreateRequest, _ *createHost) { r.Network = "host" }},
		{name: "clone mode", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspaceMode = "clone" }},
		{name: "missing command", change: func(r *runtime.CreateRequest, _ *createHost) { r.Command = nil }},
		{name: "protected environment", change: func(r *runtime.CreateRequest, _ *createHost) { r.Environment = map[string]string{"HOME": "/host/home"} }},
		{name: "secret environment", change: func(r *runtime.CreateRequest, _ *createHost) {
			r.Environment = map[string]string{"API_KEY": "not-forwarded"}
		}},
		{name: "invalid host UID", change: func(_ *runtime.CreateRequest, h *createHost) { h.UID = -1 }},
	} {
		t.Run("reject "+tt.name, func(t *testing.T) {
			request := testCreateRequest()
			host := testCreateHost()
			tt.change(&request, &host)
			got, err := buildCreateArgs(request, host)
			if err == nil || got != nil {
				t.Fatalf("invalid request returned argv = %q, error = %v", got, err)
			}
		})
	}

	t.Run("each result owns its argv", func(t *testing.T) {
		request := testCreateRequest()
		first, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		second, err := buildCreateArgs(request, testCreateHost())
		if err != nil {
			t.Fatal(err)
		}
		first[0] = "changed"
		first[len(first)-1] = "changed"
		if second[0] != "create" || second[len(second)-1] != request.Command[len(request.Command)-1] || request.Command[0] != "worker" {
			t.Fatal("argv shares mutable storage with another result or the request")
		}
	})
}

func TestCreateWorkspaceArgs(t *testing.T) {
	for _, tt := range []struct {
		name    string
		path    string
		selinux bool
		volume  string
	}{
		{name: "plain", path: "/work/project", volume: "/work/project:/workspace:rw"},
		{name: "private SELinux label", path: "/work/project", selinux: true, volume: "/work/project:/workspace:rw,Z"},
		{name: "literal path", path: "/work/a b;$HOME", selinux: true, volume: "/work/a b;$HOME:/workspace:rw,Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := createWorkspaceArgs(tt.path, tt.selinux)
			want := []string{"--volume", tt.volume, "--workdir", "/workspace"}
			if !slices.Equal(got, want) {
				t.Fatalf("workspace argv = %q, want %q", got, want)
			}
		})
	}
}

func TestCreateWorkloadArgs(t *testing.T) {
	imageID := strings.Repeat("a", 64)
	for _, tt := range []struct {
		name    string
		command []string
		want    []string
	}{
		{name: "entrypoint only", command: []string{"worker"}, want: []string{"--entrypoint", "worker", imageID}},
		{name: "literal arguments", command: []string{"program with spaces", "--privileged", "x;y", "$(whoami)", "'$HOME'", "a\nb"}, want: []string{"--entrypoint", "program with spaces", imageID, "--privileged", "x;y", "$(whoami)", "'$HOME'", "a\nb"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.command)
			got := createWorkloadArgs(tt.command, imageID)
			if !slices.Equal(got, tt.want) || !slices.Equal(tt.command, before) {
				t.Fatalf("workload argv = %q, want %q; command must stay unchanged", got, tt.want)
			}
			got[1] = "changed"
			got[len(got)-1] = "changed"
			if !slices.Equal(tt.command, before) {
				t.Fatal("workload result shares mutable storage with command")
			}
		})
	}
}

func TestParseContainerID(t *testing.T) {
	id := strings.Repeat("0123456789abcdef", 4)
	for _, tt := range []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{name: "bare ID", output: id, want: id},
		{name: "trim surrounding whitespace", output: " \t" + id + "\r\n", want: id},
		{name: "lowercase uppercase ID", output: strings.ToUpper(id), want: id},
		{name: "empty", output: "", wantErr: true},
		{name: "whitespace only", output: " \r\n", wantErr: true},
		{name: "short ID", output: id[:63], wantErr: true},
		{name: "long ID", output: id + "0", wantErr: true},
		{name: "non-hex", output: strings.Repeat("g", 64), wantErr: true},
		{name: "pending storage marker", output: strings.Repeat("0", 64), wantErr: true},
		{name: "image prefix", output: "sha256:" + id, wantErr: true},
		{name: "embedded newline", output: id[:32] + "\n" + id[32:], wantErr: true},
		{name: "multiple IDs", output: id + "\n" + id, wantErr: true},
		{name: "trailing text", output: id + " extra", wantErr: true},
		{name: "NUL byte", output: id[:63] + "\x00", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseContainerID(tt.output)
			if tt.wantErr {
				if got != "" || !errors.Is(err, ErrInvalidResponse) {
					t.Fatalf("parseContainerID = %q, %v, want empty ID and ErrInvalidResponse", got, err)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("parseContainerID = %q, %v, want %q, nil", got, err, tt.want)
			}
		})
	}
}
