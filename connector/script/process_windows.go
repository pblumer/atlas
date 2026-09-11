//go:build windows

package script

import (
	"errors"
	"os/exec"
)

// Windows keeps os/exec's direct-process cancellation. Strict sandbox mode is
// Linux-only, so a Windows execution always retains the compatible legacy path.
func configureProcessGroup(*exec.Cmd) {}

func processExists(int) error { return errors.New("process lookup unsupported") }
