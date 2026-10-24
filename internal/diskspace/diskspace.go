// Package diskspace reads how large the file system holding a directory is and how
// much of it this process may still write — the numbers the storage monitor decides
// on (ADR-0481).
//
// Each platform asks its own kernel: statfs on Linux, macOS and FreeBSD,
// GetDiskFreeSpaceExW on Windows. One reading is a single system call whose cost
// does not depend on how much is stored, which is why it may be taken every few
// seconds; walking the data directory would grow with the data and, because recovery
// checkpoints hard-link the state store's files, count the same bytes several times
// over.
//
// The numbers are what the file system reports, and that is only as true as the file
// system: a network share may report its whole pool, a host-path volume the host's
// disk, a thin-provisioned one more than the storage behind it can hold. The package
// does not guess around that; the handbook says where it happens.
package diskspace

import "errors"

// ErrUnsupported is returned on a platform this package cannot read a file system on.
var ErrUnsupported = errors.New("diskspace: reading free space is not supported on this platform")

// Usage is one reading of the file system that holds a directory, in bytes.
type Usage struct {
	// Total is the size of the file system — on Windows, of what a quota allows this
	// account.
	Total uint64
	// Free is what is not in use, including blocks the file system keeps back for the
	// superuser (ext4 keeps 5% by default).
	Free uint64
	// Available is what this process may still write: Free less those reserved blocks
	// and any quota. It is the number that runs out.
	Available uint64
	// Volume names the file system, so readings of several directories can tell
	// whether they are one disk or several. Two readings with the same Volume are the
	// same file system; the value means nothing beyond that.
	Volume string
}

// Used is what the file system holds: Total less Free.
func (u Usage) Used() uint64 {
	if u.Free > u.Total {
		return 0
	}
	return u.Total - u.Free
}

// UsedFraction is the share of the space this process can ever have that is already
// taken: Used / (Used + Available), which is how df computes its Use%. Dividing by
// Total instead would count the reserved blocks as used — on a disk with a large
// reservation that reads as nearly full while most of it is empty.
func (u Usage) UsedFraction() float64 {
	used := u.Used()
	if used+u.Available == 0 {
		return 0
	}
	return float64(used) / float64(used+u.Available)
}

// fromBlocks builds a reading from counts of blocks of one size, which is how the
// statfs family reports.
func fromBlocks(blockSize, blocks, free, available uint64, volume string) Usage {
	u := Usage{
		Total:     blocks * blockSize,
		Free:      free * blockSize,
		Available: available * blockSize,
		Volume:    volume,
	}
	return clamp(u)
}

// clamp keeps a reading ordered — Available ≤ Free ≤ Total — whatever the platform
// handed back, so a caller can subtract without checking. A quota smaller than the
// free space is the usual way to get Free above Total, and there what this account
// may use is the truth.
func clamp(u Usage) Usage {
	if u.Free > u.Total {
		u.Free = u.Available
	}
	if u.Available > u.Free {
		u.Available = u.Free
	}
	return u
}

// nonNegative converts a block count that some platforms report signed — FreeBSD's
// available count goes below zero once the reservation is in use.
func nonNegative[T int64 | uint64](v T) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
