package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/runtime"
	"go-sesh/internal/session"
	"go-sesh/internal/workspace"
)

const testID session.ID = "7ad20f51-6067-4dc4-a072-54c94d7735de"

func testView() session.View {
	return session.View{
		Record: session.Record{
			ID: testID, Name: "demo", ContainerID: strings.Repeat("b", 64), ImageID: strings.Repeat("a", 64),
			ProjectDir: "/work/project", WorkspacePath: "/work/project", WorkspaceMode: workspace.Mount, Network: runtime.NetworkNone,
		},
		Inspection: runtime.Inspection{State: runtime.Running, ContainerID: strings.Repeat("b", 64)},
	}
}

type fakeBackend struct {
	calls       []string
	ids         []session.ID
	request     session.StartRequest
	view        session.View
	views       []session.View
	err         error
	resolveErr  error
	closeErr    error
	closed      int
	execRequest session.ExecRequest
	execResult  session.ExecResult
	execFunc    func(context.Context, session.ExecRequest) (session.ExecResult, error)
}

func (b *fakeBackend) Start(_ context.Context, request session.StartRequest) (session.View, error) {
	b.calls = append(b.calls, "start")
	b.request = request
	return b.view, b.err
}

func (b *fakeBackend) Inspect(_ context.Context, id session.ID) (session.View, error) {
	b.calls = append(b.calls, "inspect")
	b.ids = append(b.ids, id)
	return b.view, b.err
}

func (b *fakeBackend) List(context.Context) ([]session.View, error) {
	b.calls = append(b.calls, "list")
	return b.views, b.err
}

func (b *fakeBackend) Stop(_ context.Context, id session.ID) (session.View, error) {
	b.calls = append(b.calls, "stop")
	b.ids = append(b.ids, id)
	return b.view, b.err
}

func (b *fakeBackend) Remove(_ context.Context, id session.ID) error {
	b.calls = append(b.calls, "remove")
	b.ids = append(b.ids, id)
	return b.err
}

func (b *fakeBackend) Exec(ctx context.Context, request session.ExecRequest) (session.ExecResult, error) {
	b.calls = append(b.calls, "exec")
	b.execRequest = request
	if b.execFunc != nil {
		return b.execFunc(ctx, request)
	}
	return b.execResult, b.err
}

func (b *fakeBackend) Resolve(_ context.Context, name string) (session.ID, error) {
	b.calls = append(b.calls, "resolve:"+name)
	return testID, b.resolveErr
}

func (b *fakeBackend) Close() error {
	b.closed++
	return b.closeErr
}

func fakeOptions(backend *fakeBackend, out, diagnostics *bytes.Buffer) Options {
	return Options{
		Stdout: out, Stderr: diagnostics, Environment: []string{"HOME=/home/test"},
		Getwd: func() (string, error) { return "/work/current", nil },
		Open:  func(string, string) (Backend, error) { return backend, nil },
	}
}

func TestRunHelpAndUsageNeverOpenBackend(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{name: "no command", code: 0},
		{name: "help", args: []string{"--help"}, code: 0},
		{name: "start help", args: []string{"start", "--help"}, code: 0},
		{name: "unknown command", args: []string{"erase"}, code: 2},
		{name: "unknown flag", args: []string{"list", "--force"}, code: 2},
		{name: "attached is not implemented", args: []string{"start", "--name", "demo", "--image", "worker", ".", "--", "worker"}, code: 2},
		{name: "required image", args: []string{"start", "--detach", "--name", "demo", ".", "--", "worker"}, code: 2},
		{name: "workload requires separator", args: []string{"start", "--detach", "--name", "demo", "--image", "worker", ".", "worker"}, code: 2},
		{name: "unsupported TTY", args: []string{"start", "--tty"}, code: 2},
		{name: "config is not loaded", args: []string{"start", "--config", "pi-sesh.json"}, code: 2},
		{name: "no selector guessing", args: []string{"inspect"}, code: 2},
		{name: "name plus ID", args: []string{"remove", "--id", string(testID), "demo"}, code: 2},
		{name: "invalid ID", args: []string{"stop", "--id", "not-a-UUID"}, code: 2},
		{name: "empty explicit ID", args: []string{"stop", "--id="}, code: 2},
		{name: "list rejects operands", args: []string{"list", "demo"}, code: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			options := Options{Stdout: &out, Stderr: &diagnostics,
				Getwd: func() (string, error) { t.Fatal("parse/help read cwd"); return "", nil },
				Open:  func(string, string) (Backend, error) { t.Fatal("parse/help opened backend"); return nil, nil },
			}
			if code := Run(context.Background(), tt.args, options); code != tt.code {
				t.Fatalf("exit = %d, want %d; stderr=%q", code, tt.code, diagnostics.String())
			}
			if tt.code == 0 && (!strings.Contains(out.String(), "sesh") || diagnostics.Len() != 0) {
				t.Fatalf("help output=%q, stderr=%q", out.String(), diagnostics.String())
			}
		})
	}
}

func TestRunStartPreservesArgvAndClosesBackend(t *testing.T) {
	backend := &fakeBackend{view: testView()}
	var out, diagnostics bytes.Buffer
	options := fakeOptions(backend, &out, &diagnostics)
	options.Open = func(state, project string) (Backend, error) {
		if state != "/work/current/state" || project != "/work/current/project" {
			t.Fatalf("open paths = %q, %q", state, project)
		}
		return backend, nil
	}
	args := []string{"start", "--detach", "--json", "--state-dir", "state", "--name", "demo", "--image", "localhost/test:1",
		"--env", "PROJECT_VALUE=a=b; $HOME", "--network", "none", "project", "--", "program with spaces", "--name", "$(whoami)"}
	code := Run(context.Background(), args, options)
	if code != 0 || diagnostics.Len() != 0 || backend.closed != 1 {
		t.Fatalf("exit=%d, close=%d, stderr=%q", code, backend.closed, diagnostics.String())
	}
	if !slices.Equal(backend.request.Command, []string{"program with spaces", "--name", "$(whoami)"}) ||
		!maps.Equal(backend.request.Environment, map[string]string{"PROJECT_VALUE": "a=b; $HOME"}) {
		t.Fatalf("literal input changed: %+v", backend.request)
	}
	var result sessionOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.ID != testID || result.State != runtime.Running {
		t.Fatalf("JSON = %q, %v", out.String(), err)
	}
}

func TestRunDispatchAndJSON(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		calls []string
	}{
		{args: []string{"inspect", "demo"}, calls: []string{"resolve:demo", "inspect"}},
		{args: []string{"inspect", string(testID)}, calls: []string{"resolve:" + string(testID), "inspect"}},
		{args: []string{"inspect", "--id", string(testID)}, calls: []string{"inspect"}},
		{args: []string{"stop", "--json", "demo"}, calls: []string{"resolve:demo", "stop"}},
		{args: []string{"--json", "remove", "demo"}, calls: []string{"resolve:demo", "remove"}},
		{args: []string{"--json", "list"}, calls: []string{"list"}},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			backend := &fakeBackend{view: testView(), views: []session.View{testView()}}
			var out, diagnostics bytes.Buffer
			options := fakeOptions(backend, &out, &diagnostics)
			options.Open = func(state, _ string) (Backend, error) {
				if state != "/home/test/.local/share/go-sesh" {
					t.Fatalf("default state is not isolated: %q", state)
				}
				return backend, nil
			}
			if code := Run(context.Background(), tt.args, options); code != 0 || !json.Valid(out.Bytes()) {
				t.Fatalf("exit=%d, output=%q, stderr=%q", code, out.String(), diagnostics.String())
			}
			if !slices.Equal(backend.calls, tt.calls) || backend.closed != 1 {
				t.Fatalf("calls=%v, close=%d", backend.calls, backend.closed)
			}
		})
	}
	t.Run("empty JSON list is an array", func(t *testing.T) {
		backend := &fakeBackend{}
		var out, diagnostics bytes.Buffer
		if code := Run(context.Background(), []string{"list", "--json"}, fakeOptions(backend, &out, &diagnostics)); code != 0 || out.String() != "[]\n" {
			t.Fatalf("exit=%d, list=%q", code, out.String())
		}
	})
}

func TestRunOutputFailureClosesBackend(t *testing.T) {
	backend := &fakeBackend{view: testView()}
	var out, diagnostics bytes.Buffer
	options := fakeOptions(backend, &out, &diagnostics)
	options.Stdout = failingWriter{err: errors.New("output closed")}
	if code := Run(context.Background(), []string{"inspect", "demo"}, options); code != 1 || backend.closed != 1 || !strings.Contains(diagnostics.String(), "output closed") {
		t.Fatalf("output failure: exit=%d, close=%d, stderr=%q", code, backend.closed, diagnostics.String())
	}
}

func TestRunFailures(t *testing.T) {
	cause := errors.New("operation failed")
	for _, kind := range []string{"operation", "resolve", "close", "cancelled", "state environment", "open"} {
		t.Run(kind, func(t *testing.T) {
			backend := &fakeBackend{view: testView()}
			var out, diagnostics bytes.Buffer
			options := fakeOptions(backend, &out, &diagnostics)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := 1
			switch kind {
			case "operation":
				backend.err = cause
			case "resolve":
				backend.resolveErr = session.ErrNotFound
			case "close":
				backend.closeErr = cause
			case "cancelled":
				cancel()
				want = 130
			case "state environment":
				options.Environment = []string{"XDG_DATA_HOME=relative"}
			case "open":
				options.Open = func(string, string) (Backend, error) { return nil, cause }
			}
			if code := Run(ctx, []string{"inspect", "demo"}, options); code != want || diagnostics.Len() == 0 {
				t.Fatalf("exit=%d, want %d; stderr=%q", code, want, diagnostics.String())
			}
			opened := kind == "operation" || kind == "resolve" || kind == "close"
			if opened && backend.closed != 1 || !opened && backend.closed != 0 {
				t.Fatalf("backend close count = %d", backend.closed)
			}
		})
	}
}
