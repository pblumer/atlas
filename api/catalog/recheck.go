package catalog

import (
	"log/slog"
	"maps"
	"slices"

	"github.com/pblumer/atlas/api/contentsource"
	"github.com/pblumer/atlas/logging"
)

// The catalogue as the recheck sees it (contentsource.Bound).
//
// What is rechecked is exactly what may be shown: the draft's binding, and the
// binding of every product in the newest release of each catalogue — the release
// the shop reads. Older releases are never rechecked, so their source content is
// never shown (HandleListReleases); a binding whose content predates a lifted
// quarantine is withheld already and is not rechecked either, or a refreshed
// product would be quarantined again by the copy its refresh replaced.

// Rechecks returns the catalogue's side of the recheck.
func (s *Service) Rechecks() contentsource.Bound { return rechecks{s: s} }

type rechecks struct{ s *Service }

// Referenced names every source a draft or a newest release binds to.
func (r rechecks) Referenced() ([]string, error) {
	var (
		out []string
		err error
	)
	r.s.loop.Do(func() {
		var bound map[string][]*ContentSource
		if bound, err = r.s.bindings(); err != nil {
			return
		}
		for _, bs := range bound {
			for _, b := range bs {
				if !slices.Contains(out, b.Source) {
					out = append(out, b.Source)
				}
			}
		}
	})
	slices.Sort(out)
	return out, err
}

// Targets lists the products bound to a source and every page each holds.
func (r rechecks) Targets(source string) ([]contentsource.Target, error) {
	var (
		out []contentsource.Target
		err error
	)
	r.s.loop.Do(func() {
		var bound map[string][]*ContentSource
		if bound, err = r.s.bindings(); err != nil {
			return
		}
		for _, item := range slices.Sorted(maps.Keys(bound)) {
			st, _, e := r.s.store.ContentState(item)
			if err = e; err != nil {
				return
			}
			t := contentsource.Target{Item: item}
			for _, b := range bound[item] {
				if b.Source != source || b.Oldest() <= st.Distrusted {
					continue
				}
				for _, c := range checksOf(b) {
					if !slices.ContainsFunc(t.Checks, func(x contentsource.Check) bool {
						return x.ID == c.ID && slices.Equal(x.Passages, c.Passages)
					}) {
						t.Checks = append(t.Checks, c)
					}
				}
			}
			if len(t.Checks) > 0 {
				out = append(out, t)
			}
		}
	})
	return out, err
}

// Record writes what a recheck found.
func (r rechecks) Record(source string, results []contentsource.Result) error {
	return r.s.record(source, results)
}

// record writes recheck findings: a pass moves the time of the last passed
// recheck and lifts nothing; a failure begins a quarantine, or leaves the one
// already running as it began.
func (s *Service) record(source string, results []contentsource.Result) error {
	var (
		err  error
		news []contentsource.Result
	)
	s.loop.Do(func() {
		for _, res := range results {
			st, _, e := s.store.ContentState(res.Item)
			if err = e; err != nil {
				return
			}
			st.ItemID, st.Source = res.Item, source
			st.LastChecked = max(st.LastChecked, res.At)
			if res.Passed {
				st.LastPassed = max(st.LastPassed, res.At)
			} else if st.Quarantine == nil {
				st.Quarantine = &Quarantine{Since: res.At, Reason: res.Reason}
				news = append(news, res)
			}
			if err = s.store.SaveContentState(st); err != nil {
				return
			}
		}
	})
	for _, res := range news {
		logging.Warn(logging.ContentQuarantined, "a product's source content is withheld from the shop until a refresh passes",
			slog.String("item", res.Item), slog.String("source", source), slog.String("reason", res.Reason))
	}
	return err
}

// bindings is every binding that may be shown, by product: the draft's and the
// newest release's of each catalogue. Runs on the loop.
func (s *Service) bindings() (map[string][]*ContentSource, error) {
	out := map[string][]*ContentSource{}
	add := func(item string, b *ContentSource) {
		if b == nil {
			return
		}
		for _, have := range out[item] {
			if sameBinding(have, b) {
				return
			}
		}
		out[item] = append(out[item], b)
	}
	items, err := s.store.Items()
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		add(it.ID, it.ContentSource)
	}
	cats, err := s.store.Catalogs()
	if err != nil {
		return nil, err
	}
	for _, c := range cats {
		rels, err := s.store.ReleasesOf(c.ID)
		if err != nil {
			return nil, err
		}
		if len(rels) == 0 {
			continue
		}
		for _, it := range rels[0].Items {
			add(it.ID, it.ContentSource)
		}
	}
	return out, nil
}

// BoundTo names the products whose draft binds to a source — what removing the
// source would leave without a recheck. Off the loop.
func (s *Service) BoundTo(source string) ([]string, error) {
	var (
		out []string
		err error
	)
	s.loop.Do(func() {
		var bound map[string][]*ContentSource
		if bound, err = s.bindings(); err != nil {
			return
		}
		for item, bs := range bound {
			for _, b := range bs {
				if b.Source == source && !slices.Contains(out, item) {
					out = append(out, item)
				}
			}
		}
	})
	slices.Sort(out)
	return out, err
}
