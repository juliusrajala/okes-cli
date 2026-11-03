package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"go-sesh/internal/cli"
	"go-sesh/internal/podman"
	"go-sesh/internal/session"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	environment := os.Environ()
	return cli.Run(ctx, args, cli.Options{
		Stdout: stdout, Stderr: stderr, Environment: environment, Getwd: os.Getwd,
		Open: func(stateDir, projectDir string) (cli.Backend, error) {
			client, err := podman.New(podman.Options{SourceEnv: environment, ProjectDir: projectDir})
			if err != nil {
				return nil, fmt.Errorf("prepare local Podman: %w", err)
			}
			store, err := session.OpenStore(stateDir)
			if err != nil {
				return nil, err
			}
			return &backend{Manager: &session.Manager{Store: store, Runtime: client}, store: store}, nil
		},
	})
}

// The frontend owns the store's lifetime. Promoted manager methods keep the CLI
// boundary small without making lifecycle code know about names or output.
type backend struct {
	*session.Manager
	store *session.Store
}

func (b *backend) Resolve(ctx context.Context, name string) (session.ID, error) {
	record, err := b.store.Resolve(ctx, name)
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

func (b *backend) Close() error {
	if err := b.store.Close(); err != nil {
		return fmt.Errorf("close session state: %w", err)
	}
	return nil
}

var _ cli.Backend = (*backend)(nil)
