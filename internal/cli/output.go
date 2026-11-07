package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"go-sesh/internal/runtime"
	"go-sesh/internal/session"
	"go-sesh/internal/workspace"
)

type executionOutput struct {
	SessionID   session.ID `json:"sessionId"`
	ExecutionID session.ID `json:"executionId"`
	ExitCode    int        `json:"exitCode"`
	Stdout      string     `json:"stdout"`
	Stderr      string     `json:"stderr"`
}

// Keep the CLI schema separate from domain structs. The saved container ID and
// observed ID remain distinct for pending recovery records and missing containers.
type sessionOutput struct {
	Name                string              `json:"name"`
	ID                  session.ID          `json:"id"`
	ContainerID         string              `json:"containerId"`
	ObservedContainerID string              `json:"observedContainerId,omitempty"`
	ImageID             string              `json:"imageId"`
	State               runtime.State       `json:"state"`
	ExitCode            *int                `json:"exitCode,omitempty"`
	ProjectDir          string              `json:"projectDir"`
	WorkspacePath       string              `json:"workspacePath"`
	Workspace           workspace.Mode      `json:"workspace"`
	Network             runtime.NetworkMode `json:"network"`
}

func outputFor(view session.View) sessionOutput {
	return sessionOutput{
		Name: view.Record.Name, ID: view.Record.ID, ContainerID: view.Record.ContainerID,
		ObservedContainerID: view.Inspection.ContainerID, ImageID: view.Record.ImageID,
		State: view.Inspection.State, ExitCode: view.Inspection.ExitCode,
		ProjectDir: view.Record.ProjectDir, WorkspacePath: view.Record.WorkspacePath,
		Workspace: view.Record.WorkspaceMode, Network: view.Record.Network,
	}
}

func writeJSON(writer io.Writer, value any) error {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return fmt.Errorf("write JSON output: %w", err)
	}
	return nil
}

func writeSession(writer io.Writer, view session.View, asJSON bool) error {
	if asJSON {
		return writeJSON(writer, outputFor(view))
	}
	exit := ""
	if view.Inspection.ExitCode != nil {
		exit = " exit=" + strconv.Itoa(*view.Inspection.ExitCode)
	}
	_, err := fmt.Fprintf(writer, "Session %s (%s) %s%s\n", view.Record.Name, view.Record.ID, view.Inspection.State, exit)
	return err
}

// writeList borrows the writer; it must not close stdout or a caller-owned file.
// Paths are quoted to keep embedded tabs/newlines from changing the table shape.
func writeList(writer io.Writer, views []session.View) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(writer, "No sessions.")
		return err
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "NAME\tSTATE\tEXIT\tSESSION ID\tWORKSPACE"); err != nil {
		return err
	}
	for _, view := range views {
		exit := "-"
		if view.Inspection.ExitCode != nil {
			exit = strconv.Itoa(*view.Inspection.ExitCode)
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%q\n", view.Record.Name, view.Inspection.State, exit, view.Record.ID, view.Record.WorkspacePath); err != nil {
			return err
		}
	}
	return table.Flush()
}
