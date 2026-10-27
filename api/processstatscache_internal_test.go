package api

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/state"
)

// The statistics endpoint folds history, and history does not change: these tests pin
// that it is not folded again for every request and every browser tab (ADR-0496).

// TestStatisticsResultIsSharedForAMinute: the same window asked twice within a minute is
// one fold; a minute later it is folded again; a different window is its own.
func TestStatisticsResultIsSharedForAMinute(t *testing.T) {
	srv, defKey, _ := statsScenario(t)
	now := int64(10000 * time.Second)
	srv.now = func() int64 { return now }
	folds := func() int {
		c := srv.statsCachesFor().results
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.folds
	}

	first := readProcessStats(t, srv, defKey, "")
	now += int64(30 * time.Second)
	second := readProcessStats(t, srv, defKey, "")
	if got := folds(); got != 1 {
		t.Fatalf("folds after two requests within a minute = %d, want 1", got)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("the shared answer differs from the first:\n%+v\n%+v", first, second)
	}

	now += int64(31 * time.Second)
	readProcessStats(t, srv, defKey, "")
	if got := folds(); got != 2 {
		t.Errorf("folds once the minute has passed = %d, want 2", got)
	}

	readProcessStats(t, srv, defKey, fmt.Sprintf("?from=%d", int64(1500*time.Second)))
	if got := folds(); got != 3 {
		t.Errorf("folds for another window = %d, want 3", got)
	}
}

// TestConcurrentStatisticsRequestsShareOneFold: five tabs asking at once wait for the one
// fold that is running instead of each starting their own.
func TestConcurrentStatisticsRequestsShareOneFold(t *testing.T) {
	c := newStatsResultCache(statsResultTTL, 8)
	key := statsResultKey{def: 1}
	release := make(chan struct{})
	var calls atomic.Int32
	compute := func() (processStatsResp, bool, error) {
		calls.Add(1)
		<-release
		return processStatsResp{Scanned: 7}, true, nil
	}

	var wg sync.WaitGroup
	got := make([]processStatsResp, 5)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _, _ = c.get(key, 1, compute)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		ready := c.folds == 1 && c.waiting == 4
		c.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the five requests never lined up behind one fold")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("folds = %d, want 1", n)
	}
	for i, r := range got {
		if r.Scanned != 7 {
			t.Errorf("request %d got %+v, want the shared answer", i, r)
		}
	}
}

// TestStatisticsFailuresAreNotKept: an error, or a definition that does not exist, is
// answered once; the next request asks again rather than being told the same for a
// minute.
func TestStatisticsFailuresAreNotKept(t *testing.T) {
	c := newStatsResultCache(statsResultTTL, 8)
	key := statsResultKey{def: 1}
	if _, _, err := c.get(key, 1, func() (processStatsResp, bool, error) {
		return processStatsResp{}, true, errors.New("store closed")
	}); err == nil {
		t.Fatal("the failure was not passed on")
	}
	if _, found, _ := c.get(key, 2, func() (processStatsResp, bool, error) {
		return processStatsResp{}, false, nil
	}); found {
		t.Fatal("a missing definition was reported found")
	}
	_, found, err := c.get(key, 3, func() (processStatsResp, bool, error) {
		return processStatsResp{Scanned: 1}, true, nil
	})
	if err != nil || !found || c.folds != 3 {
		t.Errorf("third request: found %v err %v after %d folds, want a fresh fold each time", found, err, c.folds)
	}
}

// TestStatisticsResultCacheIsBounded: one entry per window asked for, and no more than the
// bound however many windows are asked for within a minute.
func TestStatisticsResultCacheIsBounded(t *testing.T) {
	c := newStatsResultCache(statsResultTTL, 2)
	ok := func() (processStatsResp, bool, error) { return processStatsResp{}, true, nil }
	for i := 1; i <= 5; i++ {
		c.get(statsResultKey{def: uint64(i)}, int64(i), ok)
	}
	if n := len(c.entries); n > 2 {
		t.Errorf("entries = %d, want at most 2", n)
	}
	if _, ok := c.entries[statsResultKey{def: 5}]; !ok {
		t.Error("the newest window was the one dropped")
	}
}

// TestFinishedInstancesAreFoldedOnce: a finished instance's contribution is read once and
// kept; the answers are those of a fold that keeps nothing, for windows that hold an
// instance whole, cut one, or leave one out.
func TestFinishedInstancesAreFoldedOnce(t *testing.T) {
	srv, defKey, _ := statsScenario(t)
	cache := newInstanceStatsCache(100)
	fold := func(from, to int64, c *instanceStatsCache) processStatsResp {
		t.Helper()
		var resp processStatsResp
		if err := srv.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
			var err error
			resp, err = foldProcessStatistics(rv, defKey, defs[defKey].cp, from, to, maxStatisticsInstances, c)
			return err
		}); err != nil {
			t.Fatalf("fold: %v", err)
		}
		return resp
	}
	sec := func(s int64) int64 { return s * int64(time.Second) }

	for _, w := range [][2]int64{{0, sec(10000)}, {sec(1100), sec(10000)}, {sec(1500), sec(10000)}, {sec(2200), sec(2600)}, {0, sec(10000)}} {
		cached, plain := fold(w[0], w[1], cache), fold(w[0], w[1], nil)
		if !reflect.DeepEqual(cached, plain) {
			t.Errorf("window %v: kept contributions give\n%+v\nwhere a fresh fold gives\n%+v", w, cached, plain)
		}
	}
	// A and B are the finished instances. The first whole window read both (two misses);
	// [1100s, …) cuts A, which is folded for that window alone, and holds B (a hit);
	// [1500s, …) does not reach A and holds B (a hit); [2200s, 2600s) cuts B; the last
	// whole window finds both (two hits). C is still running and never kept.
	if cache.misses != 2 || cache.hits != 4 {
		t.Errorf("kept contributions: %d misses, %d hits; want 2 and 4", cache.misses, cache.hits)
	}
}

// TestInstanceStatsCacheEvictsTheLeastRecentlyUsed: the bound holds, and what goes is what
// was read longest ago.
func TestInstanceStatsCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	c := newInstanceStatsCache(2)
	a, b, d := instanceStatsKey{1, 1}, instanceStatsKey{1, 2}, instanceStatsKey{1, 3}
	c.put(a, &instanceContribution{})
	c.put(b, &instanceContribution{})
	c.get(a)
	c.put(d, &instanceContribution{})
	if _, ok := c.get(b); ok {
		t.Error("b, the least recently used, is still kept")
	}
	if _, ok := c.get(a); !ok {
		t.Error("a was read last and is gone")
	}
	if _, ok := c.get(d); !ok {
		t.Error("d, just added, is gone")
	}
}

// Requests come through the handler too: an HTTP request that names its own end is not
// confused with one that runs to now.
func TestStatisticsWindowEndingNowIsItsOwnKey(t *testing.T) {
	srv, defKey, _ := statsScenario(t)
	now := int64(10000 * time.Second)
	srv.now = func() int64 { return now }
	readProcessStats(t, srv, defKey, "")
	readProcessStats(t, srv, defKey, fmt.Sprintf("?to=%d", now))
	if code, _ := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/statistics", defKey), "", ""); code != http.StatusOK {
		t.Fatalf("statistics: %d", code)
	}
	if got := srv.statsCachesFor().results.folds; got != 2 {
		t.Errorf("folds = %d, want 2: one for the window ending now, one for the window naming its end", got)
	}
}
