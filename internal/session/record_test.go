package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

// A struct literal assigns related fields to one Record value. Tests copy this
// value, then change one field without changing the original fixture.
func validRecord() Record {
	id := ID("7ad20f51-6067-4dc4-a072-54c94d7735de")
	return Record{
		Version:       RecordVersion,
		ID:            id,
		Name:          "my-session",
		ContainerName: "pi-sesh-" + string(id),
		ContainerID:   PendingContainerID,
		ProjectDir:    "/example/project",
		WorkspaceMode: workspace.Mount,
		WorkspacePath: "/example/project",
		ImageID:       strings.Repeat("a", 64),
		Network:       runtime.NetworkNone,
		TTY:           false,
		CreatedAt:     time.Date(2026, time.October, 24, 12, 0, 0, 0, time.UTC),
	}
}

func TestRecordValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Record)
	}{
		{name: "legacy version", change: func(r *Record) { r.Version = 1 }},
		{name: "missing version", change: func(r *Record) { r.Version = 0 }},
		{name: "invalid ID", change: func(r *Record) { r.ID = "not-a-uuid" }},
		{name: "invalid name", change: func(r *Record) { r.Name = "../other" }},
		{name: "wrong container name", change: func(r *Record) { r.ContainerName = "another-container" }},
		{name: "missing container ID", change: func(r *Record) { r.ContainerID = "" }},
		{name: "bad container ID", change: func(r *Record) { r.ContainerID = strings.Repeat("g", 64) }},
		{name: "missing image ID", change: func(r *Record) { r.ImageID = "" }},
		{name: "tag instead of image ID", change: func(r *Record) { r.ImageID = "localhost/worker:latest" }},
		{name: "relative project path", change: func(r *Record) { r.ProjectDir = "relative" }},
		{name: "relative workspace path", change: func(r *Record) { r.WorkspacePath = "relative" }},
		{name: "NUL in project path", change: func(r *Record) { r.ProjectDir += "\x00" }},
		{name: "NUL in workspace path", change: func(r *Record) { r.WorkspacePath += "\x00" }},
		{name: "missing workspace mode", change: func(r *Record) { r.WorkspaceMode = "" }},
		{name: "removed clone mode", change: func(r *Record) { r.WorkspaceMode = "clone" }},
		{name: "missing network", change: func(r *Record) { r.Network = "" }},
		{name: "host network", change: func(r *Record) { r.Network = "host" }},
		{name: "missing creation time", change: func(r *Record) { r.CreatedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := validRecord()
			tt.change(&record)
			if err := record.Validate(); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("Validate error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func TestRecordValidCombinations(t *testing.T) {
	for _, mode := range []workspace.Mode{workspace.Mount, workspace.Copy} {
		for _, network := range []runtime.NetworkMode{runtime.NetworkNone, runtime.NetworkPasta} {
			t.Run(string(mode)+"/"+string(network), func(t *testing.T) {
				record := validRecord()
				record.WorkspaceMode = mode
				record.Network = network
				if err := record.Validate(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRecordKnownContainerID(t *testing.T) {
	record := validRecord()
	record.ContainerID = strings.Repeat("b", 64)
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
}
