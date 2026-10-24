package process

import (
	"errors"
	"io"
	"testing"
)

func TestCapture(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		writes    []string
		wantData  string
		wantCount int
		wantLimit bool
	}{
		{
			name: "empty output", limit: 4,
			writes: []string{""},
		},
		{
			name: "below limit", limit: 4,
			writes: []string{"a", "bc"}, wantData: "abc", wantCount: 3,
		},
		{
			name: "exact limit", limit: 4,
			writes: []string{"ab", "cd", ""}, wantData: "abcd", wantCount: 4,
		},
		{
			name: "one large write", limit: 4,
			writes: []string{"abcdef"}, wantData: "abcd", wantCount: 4, wantLimit: true,
		},
		{
			name: "overflow across writes", limit: 4,
			writes: []string{"abc", "de"}, wantData: "abcd", wantCount: 4, wantLimit: true,
		},
		{
			name: "writes after overflow", limit: 4,
			writes: []string{"abcde", "more", ""}, wantData: "abcd", wantCount: 4, wantLimit: true,
		},
		{
			name: "zero limit", limit: 0,
			writes: []string{"a"}, wantLimit: true,
		},
		{
			name: "counts bytes not characters", limit: 4,
			writes: []string{"éé", "é"}, wantData: "éé", wantCount: 4, wantLimit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overflows := 0
			output := capture{
				limit: tt.limit,
				onOverflow: func() {
					overflows++
				},
			}
			total := 0
			sawLimit := false
			for _, input := range tt.writes {
				n, err := io.WriteString(&output, input)
				if n < 0 || n > len(input) {
					t.Fatalf("Write count = %d for %d bytes", n, len(input))
				}
				if n != len(input) && err == nil {
					t.Fatal("short write without an error")
				}
				if err != nil {
					if !errors.Is(err, ErrOutputLimit) {
						t.Fatalf("Write error = %v, want ErrOutputLimit", err)
					}
					sawLimit = true
				}
				total += n
			}
			if string(output.data) != tt.wantData {
				t.Errorf("captured output = %q, want %q", output.data, tt.wantData)
			}
			if total != tt.wantCount {
				t.Errorf("total Write count = %d, want %d", total, tt.wantCount)
			}
			if sawLimit != tt.wantLimit || output.exceeded != tt.wantLimit {
				t.Errorf("limit error = %t, exceeded = %t, want %t", sawLimit, output.exceeded, tt.wantLimit)
			}
			wantOverflows := 0
			if tt.wantLimit {
				wantOverflows = 1
			}
			if overflows != wantOverflows {
				t.Errorf("overflow callbacks = %d, want %d", overflows, wantOverflows)
			}
		})
	}
}

func TestCaptureWithoutCallback(t *testing.T) {
	output := capture{limit: 1}
	if _, err := output.Write([]byte("too much")); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("Write error = %v, want ErrOutputLimit", err)
	}
}
