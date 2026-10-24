package process

import "errors"

// ErrOutputLimit indicates that captured process output exceeded its byte limit.
var ErrOutputLimit = errors.New("process output exceeded limit")

// Each output stream needs its own capture. Read data only after the process and
// its output-copy operations have finished.
type capture struct {
	data       []byte
	limit      int
	exceeded   bool
	onOverflow func()
}

func (c *capture) Write(p []byte) (int, error) {
	if c.exceeded {
		return 0, ErrOutputLimit
	}

	remaining := c.limit - len(c.data)
	if len(p) <= remaining {
		c.data = append(c.data, p...)
		return len(p), nil
	}

	c.data = append(c.data, p[:remaining]...)
	c.exceeded = true
	// A writer error alone does not stop a child that keeps producing output.
	if c.onOverflow != nil {
		c.onOverflow()
	}
	return remaining, ErrOutputLimit
}
