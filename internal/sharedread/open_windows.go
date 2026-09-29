//go:build windows

package sharedread

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// A file deleted while another reader still holds it can stay "delete pending" until
// that handle closes, and opening it meanwhile fails with ERROR_ACCESS_DENIED. Whether
// it does depends on whether the system deletes with POSIX semantics, which the name
// alone does not say. Readers hold a file for one read, so the open is tried again
// for a few milliseconds; a denial that outlasts that is a real one.
const (
	deletePendingRetries = 10
	deletePendingWait    = time.Millisecond
)

// Open opens name for reading with FILE_SHARE_DELETE in the share mode, which
// os.Open leaves out, so holding the file does not stop a delete of it or a rename
// over it.
func Open(name string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(longPath(name))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	for attempt := 0; ; attempt++ {
		h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return os.NewFile(uintptr(h), name), nil
		}
		if err != syscall.ERROR_ACCESS_DENIED || attempt == deletePendingRetries {
			return nil, &os.PathError{Op: "open", Path: name, Err: err}
		}
		time.Sleep(deletePendingWait)
	}
}

// longPath gives an absolute path at or past the length os starts prefixing at the
// \\?\ prefix, as os does for its own opens: CreateFile refuses a path longer than
// MAX_PATH without it unless the system has long paths enabled.
func longPath(name string) string {
	const prefixFrom = 248 // MAX_PATH less room for an 8.3 name, as os reckons it
	if len(name) < prefixFrom || strings.HasPrefix(name, `\\`) {
		return name
	}
	abs, err := filepath.Abs(name)
	if err != nil || strings.HasPrefix(abs, `\\`) {
		return name
	}
	return `\\?\` + abs
}
