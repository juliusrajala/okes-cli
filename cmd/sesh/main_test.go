package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpWithoutPodman(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out, diagnostics bytes.Buffer
	if code := run([]string{"--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "sesh") || diagnostics.Len() != 0 {
		t.Fatalf("help exit=%d, stdout=%q, stderr=%q", code, out.String(), diagnostics.String())
	}
}

func TestUsageDoesNotCreateState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "must-not-exist")
	var out, diagnostics bytes.Buffer
	if code := run([]string{"--state-dir", state, "start", "--tty"}, &out, &diagnostics); code != 2 {
		t.Fatalf("usage exit=%d, stderr=%q", code, diagnostics.String())
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("usage path created state: %v", err)
	}
}
