//go:build windows

package dirsync

import (
	"os"
	"syscall"
)

// Open opens dir with write access, which FlushFileBuffers requires of the
// handle it flushes. CreateFile opens a directory only with
// FILE_FLAG_BACKUP_SEMANTICS, and os.OpenFile adds that flag by itself only for
// a read-only open, so it is passed here in the high bits of flag, which Go
// hands to CreateFile as file flags.
func Open(dir string) (*os.File, error) {
	return os.OpenFile(dir, os.O_RDWR|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
}
