package api

import "fmt"

// What a new version leaves behind (ADR-0428 §5).
//
// A per-position lifecycle instance carries its right for as long as the right is
// held, and it stays on the version it started on — so does its return. Deploying a
// corrected version changes nothing for the rights already held, and the person who
// deploys it has to learn that now, while the choice is theirs: leave them on the old
// version, or migrate them (ADR-0162).

// heldOnOlderVersionsOnLoop warns, for a process a per-position product binds, how
// many instances still run on older versions of it. Reads the deployment registry,
// the engine's per-definition counters and the catalogue store, so it runs on the run
// loop (I3).
func (s *Server) heldOnOlderVersionsOnLoop(key uint64, processID string) []string {
	if s.catalogStore == nil {
		return nil
	}
	items, err := s.catalogStore.Items()
	if err != nil {
		return nil
	}
	product := ""
	for _, it := range items {
		if it.PerPosition() && it.LifecycleProcess == processID {
			product = it.ID
			break
		}
	}
	if product == "" {
		return nil
	}
	held := 0
	for k, d := range s.deployments {
		if k == key || d.ProcessID != processID {
			continue
		}
		n, err := s.store.DefInstanceCount(k)
		if err != nil {
			return nil
		}
		held += n
	}
	if held == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%d instance(s) of %s still run on older versions; product %s "+
		"runs one instance per position, so those are rights already held, and they keep the "+
		"version they were issued on — including its return. Migrate them explicitly if they "+
		"must take this version (ADR-0162, ADR-0428)", held, processID, product)}
}
