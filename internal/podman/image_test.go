package podman

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"go-sesh/internal/process"
)

func TestValidateImageReference(t *testing.T) {
	for _, image := range []string{"localhost/worker:1", "worker", "registry.example/worker@sha256:" + strings.Repeat("a", 64)} {
		if err := validateImageReference(image); err != nil {
			t.Fatal(err)
		}
	}
	for _, image := range []string{"", "--remote=true", "-pull", "hello world", "$(whoami)", "bad\x00", strings.Repeat("a", 256)} {
		if err := validateImageReference(image); err == nil {
			t.Errorf("unsafe image reference accepted: %q", image)
		}
	}
}

func TestImageExists(t *testing.T) {
	for _, status := range []int{0, 1, 2, 125} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			client := scriptClient(t, hostStep(), runnerStep{args: []string{"image", "exists", "localhost/worker:1"}, result: process.Result{ExitCode: status}})
			got, err := client.ImageExists(context.Background(), "localhost/worker:1")
			if status == 0 && (err != nil || !got) {
				t.Fatalf("exists = %t, error = %v, want true, nil", got, err)
			}
			if status == 1 && (err != nil || got) {
				t.Fatalf("exists = %t, error = %v, want false, nil", got, err)
			}
			if status != 0 && status != 1 && (!errors.Is(err, ErrCommandFailed) || got) {
				t.Fatalf("exists = %t, error = %v, want command failure", got, err)
			}
		})
	}
	t.Run("execution failure is not absence", func(t *testing.T) {
		cause := errors.New("engine unavailable")
		client := scriptClient(t, hostStep(), runnerStep{args: []string{"image", "exists", "localhost/worker:1"}, err: cause})
		got, err := client.ImageExists(context.Background(), "localhost/worker:1")
		if got || !errors.Is(err, cause) {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
	t.Run("preflight failure prevents image probe", func(t *testing.T) {
		step := hostStep()
		step.result.Stdout = strings.Replace(testHostJSON, `"rootless":true`, `"rootless":false`, 1)
		client := scriptClient(t, step)
		got, err := client.ImageExists(context.Background(), "localhost/worker:1")
		if got || !errors.Is(err, ErrUnsupportedHost) {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
	t.Run("invalid image prevents process execution", func(t *testing.T) {
		client := scriptClient(t)
		if got, err := client.ImageExists(context.Background(), "--remote=true"); got || err == nil {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
	t.Run("cancelled probe", func(t *testing.T) {
		client := scriptClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := client.ImageExists(ctx, "localhost/worker:1")
		if got || !errors.Is(err, context.Canceled) {
			t.Fatalf("exists = %t, error = %v", got, err)
		}
	})
}

func TestParseImageID(t *testing.T) {
	id := strings.Repeat("0123456789abcdef", 4)
	upperID := strings.ToUpper(id)
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{name: "bare ID", output: id, want: id},
		{name: "prefixed ID", output: "sha256:" + id, want: id},
		{name: "surrounding whitespace", output: " \t" + id + "\r\n", want: id},
		{name: "whitespace around prefixed ID", output: "\nsha256:" + id + "\n", want: id},
		{name: "preserve uppercase", output: upperID, want: upperID},
		{name: "prefixed uppercase hex", output: "sha256:" + upperID, want: upperID},
		{name: "empty", output: "", wantErr: true},
		{name: "whitespace only", output: " \t\r\n", wantErr: true},
		{name: "prefix only", output: "sha256:", wantErr: true},
		{name: "short ID", output: id[:63], wantErr: true},
		{name: "long ID", output: id + "0", wantErr: true},
		{name: "non-hex ID", output: strings.Repeat("g", 64), wantErr: true},
		{name: "different prefix", output: "sha512:" + id, wantErr: true},
		{name: "uppercase prefix", output: "SHA256:" + id, wantErr: true},
		{name: "repeated prefix", output: "sha256:sha256:" + id, wantErr: true},
		{name: "space after prefix", output: "sha256: " + id, wantErr: true},
		{name: "embedded newline", output: id[:32] + "\n" + id[32:], wantErr: true},
		{name: "multiple IDs", output: id + "\n" + id, wantErr: true},
		{name: "trailing text", output: id + " extra", wantErr: true},
		{name: "quoted ID", output: `"` + id + `"`, wantErr: true},
		{name: "NUL byte", output: id[:63] + "\x00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseImageID(tt.output)
			if tt.wantErr {
				if got != "" || !errors.Is(err, ErrInvalidResponse) {
					t.Fatalf("parseImageID = %q, %v, want empty ID and ErrInvalidResponse", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseImageID = %q, %v, want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestImageID(t *testing.T) {
	id := strings.Repeat("0123456789abcdef", 4)
	const image = "localhost/worker:1"
	inspectArgs := []string{"image", "inspect", "--format", "{{.Id}}", image}

	for _, tt := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "bare ID", output: id + "\n", want: id},
		{name: "prefixed ID", output: "sha256:" + id + "\n", want: id},
		{name: "uppercase ID", output: "\tsha256:" + strings.ToUpper(id) + "\r\n", want: strings.ToUpper(id)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host := hostStep()
			// Image inspection does not require the pasta backend.
			host.result.Stdout = strings.Replace(host.result.Stdout, `"rootlessNetworkCmd":"pasta"`, `"rootlessNetworkCmd":""`, 1)
			client := scriptClient(t, host, runnerStep{
				args:   inspectArgs,
				result: process.Result{Stdout: tt.output, Stderr: "not image data"},
			})
			got, err := client.ImageID(context.Background(), image)
			if err != nil || got != tt.want {
				t.Fatalf("ImageID = %q, %v, want %q, nil", got, err, tt.want)
			}
		})
	}

	t.Run("digest reference stays literal", func(t *testing.T) {
		reference := "registry.example/worker@sha256:" + id
		client := scriptClient(t, hostStep(), runnerStep{
			args:   []string{"image", "inspect", "--format", "{{.Id}}", reference},
			result: process.Result{Stdout: "sha256:" + id + "\n"},
		})
		got, err := client.ImageID(context.Background(), reference)
		if err != nil || got != id {
			t.Fatalf("ImageID = %q, %v, want %q, nil", got, err, id)
		}
	})

	for _, status := range []int{1, 2, 125} {
		t.Run("exit status "+strconv.Itoa(status), func(t *testing.T) {
			client := scriptClient(t, hostStep(), runnerStep{
				args:   inspectArgs,
				result: process.Result{ExitCode: status, Stdout: id},
			})
			got, err := client.ImageID(context.Background(), image)
			if got != "" || !errors.Is(err, ErrCommandFailed) {
				t.Fatalf("ImageID = %q, %v, want empty ID and ErrCommandFailed", got, err)
			}
		})
	}

	for _, cause := range []error{errors.New("engine unavailable"), context.DeadlineExceeded, process.ErrOutputLimit} {
		t.Run("execution failure: "+cause.Error(), func(t *testing.T) {
			client := scriptClient(t, hostStep(), runnerStep{
				args:   inspectArgs,
				result: process.Result{Stdout: id},
				err:    cause,
			})
			got, err := client.ImageID(context.Background(), image)
			if got != "" || !errors.Is(err, cause) {
				t.Fatalf("ImageID = %q, %v, want empty ID and wrapped %v", got, err, cause)
			}
		})
	}

	for _, output := range []string{"", "sha256:not-an-id", id + "\n" + id} {
		t.Run("invalid output: "+strconv.Quote(output), func(t *testing.T) {
			client := scriptClient(t, hostStep(), runnerStep{args: inspectArgs, result: process.Result{Stdout: output}})
			got, err := client.ImageID(context.Background(), image)
			if got != "" || !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("ImageID = %q, %v, want empty ID and ErrInvalidResponse", got, err)
			}
		})
	}

	t.Run("preflight rejection prevents image inspection", func(t *testing.T) {
		step := hostStep()
		step.result.Stdout = strings.Replace(step.result.Stdout, `"rootless":true`, `"rootless":false`, 1)
		client := scriptClient(t, step)
		got, err := client.ImageID(context.Background(), image)
		if got != "" || !errors.Is(err, ErrUnsupportedHost) {
			t.Fatalf("ImageID = %q, %v, want empty ID and ErrUnsupportedHost", got, err)
		}
	})

	t.Run("preflight execution failure prevents image inspection", func(t *testing.T) {
		cause := errors.New("host info unavailable")
		step := hostStep()
		step.err = cause
		client := scriptClient(t, step)
		got, err := client.ImageID(context.Background(), image)
		if got != "" || !errors.Is(err, cause) {
			t.Fatalf("ImageID = %q, %v, want empty ID and wrapped cause", got, err)
		}
	})

	t.Run("invalid reference prevents process execution", func(t *testing.T) {
		for _, reference := range []string{"", "--remote=true", "hello world", "$(whoami)"} {
			t.Run(strconv.Quote(reference), func(t *testing.T) {
				client := scriptClient(t)
				got, err := client.ImageID(context.Background(), reference)
				if got != "" || err == nil {
					t.Fatalf("ImageID = %q, %v, want empty ID and validation error", got, err)
				}
			})
		}
	})

	t.Run("cancelled before preflight invocation", func(t *testing.T) {
		client := scriptClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := client.ImageID(ctx, image)
		if got != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("ImageID = %q, %v, want empty ID and context.Canceled", got, err)
		}
	})
}
