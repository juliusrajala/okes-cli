package process

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Path must be absolute, it and the arguments can not be nul bytes
func validateCommand(path string, args []string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %q", path)
	}

	if strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("path contains NUL byte: %q", path)
	}

	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argument contains NUL byte: %q", arg)
		}
	}

	return nil
}
