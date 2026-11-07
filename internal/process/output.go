package process

import (
	"context"
	"errors"
	"io"
	"sync"
)

// Each outputWriter has one os/exec copy goroutine. The mutex protects only the
// borrowed destination, which can also receive writes from the other stream.
// Read err only after cmd.Run has joined both copy goroutines.
type outputWriter struct {
	capture     *capture
	destination io.Writer
	mu          *sync.Mutex
	cancel      context.CancelFunc
	err         error
}

func (w *outputWriter) Write(p []byte) (int, error) {
	accepted, captureErr := w.capture.Write(p)
	if w.destination == nil || accepted == 0 {
		return accepted, captureErr
	}
	w.mu.Lock()
	n, err := w.destination.Write(p[:accepted])
	w.mu.Unlock()
	if n < 0 || n > accepted {
		n = 0
		err = io.ErrShortWrite
	} else if n != accepted && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, errors.Join(captureErr, err)
}
