package api

import (
	"strings"

	"github.com/pblumer/atlas/api/capability"
	"github.com/pblumer/atlas/api/catalog"
)

// capabilityHeadings answers the catalogue's question about the capability register:
// what a structured catalogue's category and product group name
// (ADR-draft-marktleistung-and-service). The register is the server's, so the
// catalogue asks through this rather than holding the store, the way it asks about
// deployments through processLookup.
type capabilityHeadings struct{ s *Server }

// CapabilityHeading reads one capability on the loop, where the register's store is
// read by its own service. A design-time record of a few fields: one visit per key,
// and the catalogue caches the answers for the length of a publish.
func (l capabilityHeadings) CapabilityHeading(key string) (catalog.CapabilityHeading, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || l.s.capabilityRecords == nil {
		return catalog.CapabilityHeading{}, false, nil
	}
	var (
		out   catalog.CapabilityHeading
		found bool
		err   error
	)
	l.s.do(func() {
		c, ok, e := l.s.capabilityRecords.Get(key)
		if e != nil || !ok {
			found, err = ok, e
			return
		}
		found = true
		out = catalog.CapabilityHeading{
			Name:       c.Name,
			Requires:   append([]string(nil), c.Requires...),
			Deprecated: c.State == capability.StateDeprecated,
		}
	})
	return out, found, err
}
