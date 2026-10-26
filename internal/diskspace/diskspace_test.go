package diskspace

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestUsedFractionIsDfsUsePercent: the share is Used / (Used + Available), as df
// computes it. The case that motivates it is a disk with a large reservation: this
// very development container reports 252 GiB in total, 243 GiB free and 29.5 GiB
// available. Divided by Total, that reads 88% full; df says 24%, and df is right.
func TestUsedFractionIsDfsUsePercent(t *testing.T) {
	const gib = 1 << 30
	cases := []struct {
		name string
		u    Usage
		want float64
	}{
		{"a reservation larger than what is available", Usage{Total: 252 * gib, Free: 243 * gib, Available: 29.5 * gib}, 9.0 / 38.5},
		{"no reservation", Usage{Total: 100, Free: 25, Available: 25}, 0.75},
		{"full", Usage{Total: 100, Free: 5, Available: 0}, 1},
		{"empty reading", Usage{}, 0},
		{"free above total", Usage{Total: 10, Free: 20, Available: 0}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.u.UsedFraction(); math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("UsedFraction() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestClampKeepsAReadingOrdered: whatever a platform hands back, Available ≤ Free ≤
// Total, so a caller may subtract. A Windows quota below the free space is how Free
// ends up above Total; what the quota leaves this account is then the truth.
func TestClampKeepsAReadingOrdered(t *testing.T) {
	cases := []struct {
		name string
		in   Usage
		want Usage
	}{
		{"ordered", Usage{Total: 100, Free: 50, Available: 40}, Usage{Total: 100, Free: 50, Available: 40}},
		{"quota below free space", Usage{Total: 10, Free: 500, Available: 4}, Usage{Total: 10, Free: 4, Available: 4}},
		{"available above free", Usage{Total: 100, Free: 30, Available: 60}, Usage{Total: 100, Free: 30, Available: 30}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clamp(c.in); got != c.want {
				t.Fatalf("clamp(%+v) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestFromBlocksMultipliesByTheBlockSize(t *testing.T) {
	got := fromBlocks(4096, 1000, 400, 300, "dev:1")
	want := Usage{Total: 4096000, Free: 1638400, Available: 1228800, Volume: "dev:1"}
	if got != want {
		t.Fatalf("fromBlocks = %+v, want %+v", got, want)
	}
}

func TestNonNegativeClampsASignedCount(t *testing.T) {
	if got := nonNegative(int64(-5)); got != 0 {
		t.Fatalf("nonNegative(-5) = %d, want 0", got)
	}
	if got := nonNegative(int64(7)); got != 7 {
		t.Fatalf("nonNegative(7) = %d, want 7", got)
	}
	if got := nonNegative(uint64(9)); got != 9 {
		t.Fatalf("nonNegative(9) = %d, want 9", got)
	}
	if u := (Usage{Total: 5, Free: 9}); u.Used() != 0 {
		t.Fatalf("Used() with Free above Total = %d, want 0", u.Used())
	}
}

// TestOfReadsTheFileSystemHoldingADirectory reads the real disk under a temp dir.
// Its numbers are whatever this machine has, so only their shape is asserted.
func TestOfReadsTheFileSystemHoldingADirectory(t *testing.T) {
	dir := t.TempDir()
	u, err := Of(dir)
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	if u.Total == 0 {
		t.Fatalf("Total = 0 for %s", dir)
	}
	if u.Available > u.Free || u.Free > u.Total {
		t.Fatalf("reading out of order: %+v", u)
	}
	if u.Volume == "" {
		t.Fatal("Volume is empty")
	}

	// A subdirectory is on the same file system, and says so.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	v, err := Of(sub)
	if err != nil {
		t.Fatalf("Of(sub): %v", err)
	}
	if v.Volume != u.Volume {
		t.Fatalf("a subdirectory reads as volume %q, its parent as %q", v.Volume, u.Volume)
	}
}

func TestOfAMissingDirectoryFails(t *testing.T) {
	_, err := Of(filepath.Join(t.TempDir(), "absent"))
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err == nil {
		t.Fatal("Of of a missing directory succeeded")
	}
}
