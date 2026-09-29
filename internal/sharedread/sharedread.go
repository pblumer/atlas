// Package sharedread opens a file for reading without stopping anyone else from
// deleting it or renaming another file over it — the latter, on Windows, with a
// rename that asks for POSIX semantics, as os.Root.Rename does.
//
// On Unix every open already behaves that way. Windows is why this package exists:
// os.Open leaves FILE_SHARE_DELETE out of the share mode, so while a reader holds a
// file, a delete of it or a rename over it fails with "The process cannot access the
// file because it is being used by another process". A design-time store reads off
// the run loop while the writer on it deletes and replaces records (api/sidecar), and
// on Windows those two collided.
package sharedread

import "io"

// ReadFile reads the whole file at name, as os.ReadFile does, through Open.
func ReadFile(name string) ([]byte, error) {
	f, err := Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
