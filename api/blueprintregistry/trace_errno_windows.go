//go:build windows

package blueprintregistry

import (
	"errors"
	"syscall"
)

// Winsock reports its own codes, which syscall.ECONNREFUSED and friends do not
// match on Windows: WSAECONNREFUSED, WSAENETUNREACH and WSAEHOSTUNREACH.
const (
	wsaeConnRefused syscall.Errno = 10061
	wsaeNetUnreach  syscall.Errno = 10051
	wsaeHostUnreach syscall.Errno = 10065
)

func isRefused(err error) bool { return errors.Is(err, wsaeConnRefused) }

func isUnreachable(err error) bool {
	return errors.Is(err, wsaeNetUnreach) || errors.Is(err, wsaeHostUnreach)
}
