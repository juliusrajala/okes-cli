package podman

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

func testCreateRequest() runtime.CreateRequest {
	return runtime.CreateRequest{
		Target:        testTarget(false),
		Name:          "checks",
		ImageID:       strings.Repeat("a", 64),
		WorkspacePath: "/work/project",
		WorkspaceMode: workspace.Mount,
		Network:       runtime.NetworkNone,
		Command:       []string{"worker", "--literal", "argument with spaces", "$(touch /tmp/not-executed)"},
		Environment:   map[string]string{"PROJECT_PROFILE": "local", "NODE_ENV": "test"},
	}
}

func testCreateHost() createHost {
	return createHost{UID: 1000, GID: 1001}
}

func TestValidateCreateRequest(t *testing.T) {
	request := testCreateRequest()
	before := request
	before.Command = slices.Clone(request.Command)
	before.Environment = maps.Clone(request.Environment)
	entries, err := validateCreateRequest(request, testCreateHost())
	if err != nil || !slices.Equal(entries, []string{"NODE_ENV=test", "PROJECT_PROFILE=local"}) {
		t.Fatalf("validation = %q, %v, want sorted container environment", entries, err)
	}
	if !reflect.DeepEqual(request, before) {
		t.Fatal("validation modified the request")
	}

	for _, tt := range []struct {
		name   string
		change func(*runtime.CreateRequest, *createHost)
	}{
		{name: "empty session ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.Target.SessionID = "" }},
		{name: "NUL in session ID", change: func(r *runtime.CreateRequest, _ *createHost) {
			r.Target.SessionID = "bad\x00id"
			r.Target.ContainerName = "pi-sesh-" + r.Target.SessionID
		}},
		{name: "mismatched container name", change: func(r *runtime.CreateRequest, _ *createHost) { r.Target.ContainerName = "unrelated" }},
		{name: "bound container ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.Target.ContainerID = strings.Repeat("b", 64) }},
		{name: "pending storage marker", change: func(r *runtime.CreateRequest, _ *createHost) { r.Target.ContainerID = strings.Repeat("0", 64) }},
		{name: "empty alias", change: func(r *runtime.CreateRequest, _ *createHost) { r.Name = "" }},
		{name: "alias starts with option", change: func(r *runtime.CreateRequest, _ *createHost) { r.Name = "--privileged" }},
		{name: "alias has newline", change: func(r *runtime.CreateRequest, _ *createHost) { r.Name = "bad\nname" }},
		{name: "alias too long", change: func(r *runtime.CreateRequest, _ *createHost) { r.Name = strings.Repeat("a", 65) }},
		{name: "empty image ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = "" }},
		{name: "tag instead of image ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = "localhost/worker:1" }},
		{name: "prefixed image ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = "sha256:" + r.ImageID }},
		{name: "non-hex image ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = strings.Repeat("g", 64) }},
		{name: "short image ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = r.ImageID[:63] }},
		{name: "empty workspace path", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "" }},
		{name: "relative workspace path", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "work/project" }},
		{name: "unclean workspace path", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "/work/project/../other" }},
		{name: "colon in volume source", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "/work/project:ro" }},
		{name: "comma in volume source", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "/work/project,ro" }},
		{name: "NUL in volume source", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "/work/project\x00" }},
		{name: "missing workspace mode", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspaceMode = "" }},
		{name: "obsolete clone mode", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspaceMode = "clone" }},
		{name: "missing network mode", change: func(r *runtime.CreateRequest, _ *createHost) { r.Network = "" }},
		{name: "host networking", change: func(r *runtime.CreateRequest, _ *createHost) { r.Network = "host" }},
		{name: "negative UID", change: func(_ *runtime.CreateRequest, h *createHost) { h.UID = -1 }},
		{name: "negative GID", change: func(_ *runtime.CreateRequest, h *createHost) { h.GID = -1 }},
		{name: "nil command", change: func(r *runtime.CreateRequest, _ *createHost) { r.Command = nil }},
		{name: "empty entrypoint", change: func(r *runtime.CreateRequest, _ *createHost) { r.Command = []string{"", "argument"} }},
		{name: "empty argument", change: func(r *runtime.CreateRequest, _ *createHost) { r.Command = []string{"worker", ""} }},
		{name: "NUL in argument", change: func(r *runtime.CreateRequest, _ *createHost) { r.Command = []string{"worker", "bad\x00argument"} }},
		{name: "protected environment", change: func(r *runtime.CreateRequest, _ *createHost) { r.Environment = map[string]string{"HOME": "/host/home"} }},
		{name: "secret environment", change: func(r *runtime.CreateRequest, _ *createHost) {
			r.Environment = map[string]string{"ACCESS_TOKEN": "not-forwarded"}
		}},
		{name: "NUL in TTY TERM", change: func(r *runtime.CreateRequest, h *createHost) { r.TTY = true; h.Term = "bad\x00term" }},
		{name: "NUL in TTY COLORTERM", change: func(r *runtime.CreateRequest, h *createHost) { r.TTY = true; h.ColorTerm = "bad\x00color" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := testCreateRequest()
			host := testCreateHost()
			tt.change(&request, &host)
			entries, err := validateCreateRequest(request, host)
			if err == nil || entries != nil {
				t.Fatalf("invalid request returned entries = %q, error = %v", entries, err)
			}
		})
	}

	for _, tt := range []struct {
		name   string
		change func(*runtime.CreateRequest, *createHost)
	}{
		{name: "copy path describes preparation elsewhere", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspaceMode = workspace.Copy }},
		{name: "explicit pasta", change: func(r *runtime.CreateRequest, _ *createHost) { r.Network = runtime.NetworkPasta }},
		{name: "uppercase image ID", change: func(r *runtime.CreateRequest, _ *createHost) { r.ImageID = strings.Repeat("A", 64) }},
		{name: "maximum alias length", change: func(r *runtime.CreateRequest, _ *createHost) { r.Name = strings.Repeat("a", 64) }},
		{name: "literal shell characters in path", change: func(r *runtime.CreateRequest, _ *createHost) { r.WorkspacePath = "/work/a b;$HOME" }},
		{name: "nil environment", change: func(r *runtime.CreateRequest, _ *createHost) { r.Environment = nil }},
		{name: "terminal ignored without TTY", change: func(_ *runtime.CreateRequest, h *createHost) { h.Term = "ignored\x00term" }},
		{name: "stdin without TTY", change: func(r *runtime.CreateRequest, _ *createHost) { r.Interactive = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := testCreateRequest()
			host := testCreateHost()
			tt.change(&request, &host)
			if _, err := validateCreateRequest(request, host); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContainerEnvironment(t *testing.T) {
	environment := map[string]string{"Z_VALUE": "literal=$HOME; $(whoami)", "A_VALUE": "", "_MODE": "développement"}
	before := maps.Clone(environment)
	got, err := containerEnvironment(environment)
	want := []string{"A_VALUE=", "Z_VALUE=literal=$HOME; $(whoami)", "_MODE=développement"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("environment = %q, %v, want %q, nil", got, err, want)
	}
	if !maps.Equal(environment, before) {
		t.Fatal("validation modified the environment")
	}

	for _, name := range []string{
		"", "lowercase", "1START", "HAS-DASH", "HAS=EQUALS", "HAS\x00NUL", strings.Repeat("A", 65),
		"HOME", "PATH", "PWD", "OLDPWD", "TERM", "COLORTERM", "LANG", "LC_ALL",
		"NODE_OPTIONS", "BASH_ENV", "ENV", "SHELLOPTS", "NODE_TLS_REJECT_UNAUTHORIZED",
		"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"XDG_CONFIG_HOME", "PI_SESH_SESSION", "LD_PRELOAD", "OPENAI_MODEL", "ANTHROPIC_MODEL",
		"GOOGLE_GENERATIVE_AI_MODEL", "AZURE_OPENAI_MODEL", "AWS_ENDPOINT_URL_S3",
		"GIT_CONFIG_COUNT", "GIT_ASKPASS", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_EXEC_PATH", "GIT_EXTERNAL_DIFF", "GIT_PAGER",
		"SSH_ASKPASS", "SSH_AUTH_SOCK", "API_KEY", "MY_API_KEY_VALUE", "ACCESS_KEY", "TOKEN", "APP_TOKEN",
		"SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "CREDENTIALS", "AUTH", "APP_AUTH_MODE", "DATABASE_URL",
	} {
		t.Run("reject name "+fmt.Sprintf("%q", name), func(t *testing.T) {
			entries, err := containerEnvironment(map[string]string{name: "synthetic-private-value"})
			if err == nil || entries != nil {
				t.Fatalf("unsafe variable returned entries = %q, error = %v", entries, err)
			}
			if strings.Contains(err.Error(), "synthetic-private-value") {
				t.Fatal("error contains an environment value")
			}
		})
	}
	for _, value := range []string{"bad\x00value", "bad\nvalue", "bad\rvalue"} {
		t.Run("reject multiline or NUL "+fmt.Sprintf("%q", value), func(t *testing.T) {
			entries, err := containerEnvironment(map[string]string{"PROJECT_VALUE": value})
			if err == nil || entries != nil {
				t.Fatalf("invalid value returned entries = %q, error = %v", entries, err)
			}
			if strings.Contains(err.Error(), "bad") {
				t.Fatal("error contains an environment value")
			}
		})
	}
	for _, name := range []string{"_", strings.Repeat("A", 64), "AUTHORS", "TOKENIZER", "MY_SECRETARY", "NODE_ENV", "PROJECT_PROFILE"} {
		t.Run("allow name "+name, func(t *testing.T) {
			if _, err := containerEnvironment(map[string]string{name: "literal"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContainerEnvironmentLimits(t *testing.T) {
	entries := make(map[string]string)
	for i := range 64 {
		entries[fmt.Sprintf("VALUE_%02d", i)] = ""
	}
	excessEntries := maps.Clone(entries)
	excessEntries["VALUE_EXTRA"] = ""
	// Four one-byte names each add two bytes for the name and '='.
	exactTotal := map[string]string{
		"A": strings.Repeat("x", 4096),
		"B": strings.Repeat("x", 4096),
		"C": strings.Repeat("x", 4096),
		"D": strings.Repeat("x", 4088),
	}
	excessTotal := maps.Clone(exactTotal)
	excessTotal["D"] += "x"

	for _, tt := range []struct {
		name        string
		environment map[string]string
		wantErr     bool
	}{
		{name: "nil map", environment: nil},
		{name: "64 entries", environment: entries},
		{name: "65 entries", environment: excessEntries, wantErr: true},
		{name: "4096 value bytes", environment: map[string]string{"VALUE": strings.Repeat("x", 4096)}},
		{name: "4097 value bytes", environment: map[string]string{"VALUE": strings.Repeat("x", 4097)}, wantErr: true},
		{name: "4096 UTF-8 bytes", environment: map[string]string{"VALUE": strings.Repeat("é", 2048)}},
		{name: "4098 UTF-8 bytes", environment: map[string]string{"VALUE": strings.Repeat("é", 2049)}, wantErr: true},
		{name: "16384 total bytes", environment: exactTotal},
		{name: "16385 total bytes", environment: excessTotal, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := containerEnvironment(tt.environment)
			if tt.wantErr {
				if err == nil || got != nil {
					t.Fatalf("invalid environment returned %d entries, error = %v", len(got), err)
				}
			} else if err != nil || len(got) != len(tt.environment) {
				t.Fatalf("environment returned %d entries, error = %v", len(got), err)
			}
		})
	}
}
