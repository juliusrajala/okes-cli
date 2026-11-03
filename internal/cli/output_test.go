package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go-sesh/internal/runtime"
	"go-sesh/internal/session"
)

// failingWriter is a real io.Writer implementation. It does not need an
// inheritance declaration; Write's signature supplies the interface contract.
type failingWriter struct{ err error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestOutputJSON(t *testing.T) {
	view := testView()
	view.Record.ContainerID = session.PendingContainerID
	view.Inspection.State = runtime.Exited
	code := 0
	view.Inspection.ExitCode = &code
	var out bytes.Buffer
	if err := writeJSON(&out, outputFor(view)); err != nil {
		t.Fatal(err)
	}
	var result struct {
		sessionOutput
		Environment json.RawMessage `json:"environment"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ContainerID != session.PendingContainerID || result.ObservedContainerID != view.Inspection.ContainerID || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("saved and observed state were mixed: %s", out.String())
	}
	if result.Environment != nil {
		t.Fatal("output exposes container environment")
	}
	cause := errors.New("closed output stream")
	if err := writeJSON(failingWriter{err: cause}, outputFor(view)); !errors.Is(err, cause) {
		t.Fatalf("writer failure was lost: %v", err)
	}
}

func TestWriteList(t *testing.T) {
	view := testView()
	view.Record.WorkspacePath = "/work/name\twith\ncontrol-characters"
	var out bytes.Buffer
	if err := writeList(&out, []session.View{view}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "NAME") || !strings.Contains(out.String(), "demo") ||
		!strings.Contains(out.String(), `\twith\n`) || strings.Count(out.String(), "\n") != 2 {
		t.Fatalf("invalid table output: %q", out.String())
	}
	out.Reset()
	if err := writeList(&out, nil); err != nil || out.String() != "No sessions.\n" {
		t.Fatalf("empty list = %q, %v", out.String(), err)
	}
}

func TestWriteListFailure(t *testing.T) {
	cause := errors.New("output stream failed")
	for _, tt := range []struct {
		name  string
		views []session.View
	}{
		{name: "buffered table", views: []session.View{testView()}},
		{name: "empty list", views: []session.View{}},
		{name: "nil list"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := writeList(failingWriter{err: cause}, tt.views)
			if !errors.Is(err, cause) {
				t.Fatalf("writeList error = %v, want %v", err, cause)
			}
		})
	}
}
