//go:build !linux

package script

import (
	"fmt"
	"runtime"
)

func sandboxSupport() error {
	return fmt.Errorf("script sandbox: strict mode is unsupported on %s", runtime.GOOS)
}

func runSandbox(string, []string, []string) error { return sandboxSupport() }

func sandboxRuntimeRoots() []string { return nil }
