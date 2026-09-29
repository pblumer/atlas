// Package dirsync opens a directory so that fsyncing it makes the creates,
// renames and removes inside it durable — the step the WAL, checkpoints and
// every design-time store take after changing a directory's entries.
//
// On Unix that is just os.Open. Windows is why this package exists: os.Open
// gives a directory a read-only handle, and FlushFileBuffers requires a handle
// with write access, so the fsync is refused with "Access is denied". Opening
// the directory is the one step that differs, so it is the one step that lives
// here; each caller keeps its own Sync and its own error wording.
package dirsync
