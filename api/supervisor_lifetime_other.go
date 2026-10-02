//go:build !windows

package api

import "os"

// childLifetime is Windows' job object elsewhere, and does nothing. A worker
// outliving its server there is the platform's to prevent — systemd's control group,
// a container's PID namespace — and Atlas's supervisor is documented as declining to
// fight a platform that owns process lifecycle (ADR-0157)
// (ADR-0439).
type childLifetime struct{}

func (*childLifetime) bind(*os.Process) error { return nil }

func (*childLifetime) close() {}
