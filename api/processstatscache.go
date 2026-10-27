package api

import (
	"container/list"
	"sync"
	"time"
)

// What the statistics endpoint keeps between requests (ADR-0496).
//
// The figures a heatmap shows describe history, and history is the part of the store
// that does not change: a finished instance's trail is fixed the moment it ends. Folding
// it again for every request — and for every browser tab asking the same question — is
// what made the endpoint cost the server CPU and garbage in proportion to the operators
// looking at it. Two caches answer the two halves of that:
//
//   - a result is shared for statsResultTTL: every request for the same window within a
//     minute gets the one fold, and requests that arrive while it runs wait for it rather
//     than starting their own;
//   - a finished instance's contribution is folded once and kept (instanceStatsCache), so
//     even a fresh fold re-reads only the instances still running and those the window
//     cuts.

const (
	// statsResultTTL is how long a statistics result is shared. A heatmap is read in
	// minutes and its medians move in hours; a minute-old answer says nothing different.
	statsResultTTL = int64(time.Minute)
	// statsResultMax bounds the shared results, one per (definition, window).
	statsResultMax = 256
	// statsInstanceCacheMax bounds the kept contributions: twice the instances one fold
	// may read, so a full window fits with room for a second definition's.
	statsInstanceCacheMax = 2 * maxStatisticsInstances
)

// processStatsCaches is the statistics endpoint's memory.
type processStatsCaches struct {
	results   *statsResultCache
	instances *instanceStatsCache
}

// statsCachesFor returns the server's statistics caches, built on first use.
func (s *Server) statsCachesFor() *processStatsCaches {
	s.statsCachesOnce.Do(func() {
		s.statsCaches = &processStatsCaches{
			results:   newStatsResultCache(statsResultTTL, statsResultMax),
			instances: newInstanceStatsCache(statsInstanceCacheMax),
		}
	})
	return s.statsCaches
}

// statsResultKey names one window of one definition. to is 0 for a window that ends
// "now", so requests a few seconds apart share it.
type statsResultKey struct {
	def      uint64
	from, to int64
}

// statsResult is one fold's answer, or the fold still running: done is closed once the
// fields are set, and at is 0 until then.
type statsResult struct {
	done  chan struct{}
	at    int64
	resp  processStatsResp
	found bool
	err   error
}

// statsResultCache shares statistics results for ttl and lets concurrent requests for the
// same window wait on one fold.
type statsResultCache struct {
	mu      sync.Mutex
	ttl     int64
	max     int
	entries map[statsResultKey]*statsResult
	folds   int // results computed rather than shared; read by tests
	waiting int // requests currently waiting on another's fold; read by tests
}

func newStatsResultCache(ttl int64, max int) *statsResultCache {
	return &statsResultCache{ttl: ttl, max: max, entries: map[statsResultKey]*statsResult{}}
}

// get answers key from a result computed less than ttl before now, from the fold already
// running for it, or from compute. A failure or a definition that does not exist is
// answered once and never kept: the next request asks again.
func (c *statsResultCache) get(key statsResultKey, now int64, compute func() (processStatsResp, bool, error)) (processStatsResp, bool, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && (e.at == 0 || now-e.at < c.ttl) {
		c.waiting++
		c.mu.Unlock()
		<-e.done
		c.mu.Lock()
		c.waiting--
		c.mu.Unlock()
		return e.resp, e.found, e.err
	}
	c.sweep(now)
	e := &statsResult{done: make(chan struct{})}
	c.entries[key] = e
	c.folds++
	c.mu.Unlock()

	resp, found, err := compute()

	c.mu.Lock()
	e.resp, e.found, e.err, e.at = resp, found, err, now
	if (err != nil || !found) && c.entries[key] == e {
		delete(c.entries, key)
	}
	c.mu.Unlock()
	close(e.done)
	return resp, found, err
}

// sweep drops expired results and, if the cache is still full, the oldest finished one.
// A fold still running is never dropped: requests are waiting on it. Called with mu held.
func (c *statsResultCache) sweep(now int64) {
	for k, e := range c.entries {
		if e.at != 0 && now-e.at >= c.ttl {
			delete(c.entries, k)
		}
	}
	for len(c.entries) >= c.max {
		var (
			oldest    statsResultKey
			oldestAt  int64
			candidate bool
		)
		for k, e := range c.entries {
			if e.at != 0 && (!candidate || e.at < oldestAt) {
				oldest, oldestAt, candidate = k, e.at, true
			}
		}
		if !candidate {
			return
		}
		delete(c.entries, oldest)
	}
}

// instanceStatsKey names one instance's contribution. The definition is part of it
// because a contribution is in that definition's element and flow indices.
type instanceStatsKey struct{ def, pi uint64 }

type instanceStatsEntry struct {
	key instanceStatsKey
	c   *instanceContribution
}

// instanceStatsCache keeps finished instances' contributions, least recently used first
// out. Nothing invalidates an entry because nothing can change one: a finished instance's
// trail is fixed, an instance key is never reused, and a purged instance is simply never
// asked for again and ages out.
type instanceStatsCache struct {
	mu           sync.Mutex
	max          int
	order        *list.List // front: most recently used
	items        map[instanceStatsKey]*list.Element
	hits, misses int // read by tests
}

func newInstanceStatsCache(max int) *instanceStatsCache {
	return &instanceStatsCache{max: max, order: list.New(), items: map[instanceStatsKey]*list.Element{}}
}

func (c *instanceStatsCache) get(k instanceStatsKey) (*instanceContribution, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.order.MoveToFront(el)
	return el.Value.(*instanceStatsEntry).c, true
}

func (c *instanceStatsCache) put(k instanceStatsKey, contrib *instanceContribution) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*instanceStatsEntry).c = contrib
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&instanceStatsEntry{key: k, c: contrib})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*instanceStatsEntry).key)
	}
}
