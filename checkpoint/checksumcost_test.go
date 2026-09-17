package checkpoint

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkChecksumDirBySize is the evidence behind splitting Publish into Stage and
// Commit (ADR-draft-whole-store-reads-leave-the-writer).
//
// ChecksumDir reads every file in the checkpoint, which is every SST file in the state
// store, so its cost is the store's size and nothing else. That is the number that
// decided where it may run: inside the run-loop turn that takes the snapshot, it is
// exactly this many seconds of a server that answers nothing.
//
// The rate here is the best case — the files are in the page cache by the second
// iteration, so a cold store is slower, not faster.
//
//	go test ./checkpoint -run '^$' -bench 'ChecksumDirBySize' -benchtime=3x
func BenchmarkChecksumDirBySize(b *testing.B) {
	for _, mb := range []int{64, 512} {
		dir := b.TempDir()
		// Pebble's SSTables start at ~2MB in L0 and double per level; 32MB files keep
		// the file count sane while measuring the same bytes.
		const fileMB = 32
		buf := make([]byte, fileMB<<20)
		if _, err := rand.Read(buf); err != nil {
			b.Fatal(err)
		}
		for i := range mb / fileMB {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%06d.sst", i)), buf, 0o644); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("%dMB", mb), func(b *testing.B) {
			b.SetBytes(int64(mb) << 20)
			for range b.N {
				if _, err := ChecksumDir(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
