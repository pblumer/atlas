package state

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
)

// A one-off measurement, not part of the suite. Run with:
//
//	go test ./state/ -run TestMeasureEntitlementCheckpoint -v -count=1 -tags=measure
func TestMeasureEntitlementCheckpoint(t *testing.T) {
	if os.Getenv("ATLAS_MEASURE") == "" {
		t.Skip("set ATLAS_MEASURE=1")
	}
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const people = 100_000
	const perPerson = 20 // 2 million rights
	start := time.Now()
	for p := 0; p < people; p++ {
		tx := s.NewTransaction()
		principal := fmt.Sprintf("usr_%024x", p)
		for i := 0; i < perPerson; i++ {
			must(t, tx.PutEntitlement(&model.EntitlementValue{
				Principal: principal,
				ItemID:    fmt.Sprintf("item-%s-%03d", "microsoft-365-e5-lizenz", i),
				VariantID: "standard",
				OrderID:   fmt.Sprintf("ord_%024x", p*perPerson+i),
				Since:     1_700_000_000_000_000_000,
				Origin:    model.OriginOrdered,
			}))
		}
		commit(t, tx)
	}
	t.Logf("wrote %d entitlements in %s", people*perPerson, time.Since(start).Round(time.Millisecond))

	n, err := s.EntitlementCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	t.Logf("EntitlementCount()=%d took %s", n, time.Since(start).Round(time.Millisecond))

	cpStart := time.Now()
	dest := filepath.Join(t.TempDir(), "cp")
	if err := s.Snapshot(dest); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	t.Logf("checkpoint in %s", time.Since(cpStart).Round(time.Millisecond))
	t.Logf("live store %s, checkpoint %s", humanSize(t, dir), humanSize(t, dest))
	shared, total := linked(t, dest)
	t.Logf("checkpoint sst files that are hard links: %d of %d", shared, total)

	readStart := time.Now()
	held := 0
	if err := s.EntitlementsOf(fmt.Sprintf("usr_%024x", people/2), func(*model.EntitlementValue) error {
		held++
		return nil
	}); err != nil {
		t.Fatalf("EntitlementsOf: %v", err)
	}
	t.Logf("one person's %d rights read in %s", held, time.Since(readStart).Round(time.Microsecond))
}

// linked counts how many of a checkpoint's sst files are hard links to the live
// store's, which is what decides whether the 19ms above is a measurement of
// copying or of linking.
func linked(t *testing.T, dir string) (int, int) {
	t.Helper()
	shared, total := 0, 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".sst" {
			continue
		}
		total++
		fi, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			shared++
		}
	}
	return shared, total
}

func humanSize(t *testing.T, dir string) string {
	t.Helper()
	var total int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return fmt.Sprintf("%.1f MiB", float64(total)/(1<<20))
}
