package archsketch

import "slices"

// Minimal is the snapshot as a reader outside Atlas may see it: a page in another
// system that shows the sketch to everybody who can open that page (ADR-0493, as
// amended for the architecture sketch). Everything a read withholds from some reader
// of the release (ADR-0495 §5) is withheld here from every reader:
//
//   - a Worker keeps its name and Worker Type and loses its endpoints, the rest of its
//     configuration and its inbound subscriptions' subjects, and the instances seen
//     serving it are counted rather than named;
//   - a catalogue product is counted rather than shown;
//   - another application, called or coupled through a name, is "another
//     application", and a feed endpoint is withheld;
//   - the owner of a capability or a value stream is withheld, name and role, because
//     a person is not what such a page shows.
//
// The release's own application keeps its name: it is what the page names. The
// snapshot it is called on is not changed, and the result is what every reader gets,
// so it depends on nothing but the snapshot.
func (s Snapshot) Minimal(applicationID string) Snapshot {
	none := func(string) bool { return false }
	out := s
	if s.Physical != nil {
		p := s.Physical.Redacted(none, false)
		out.Physical = &p
	}
	if s.Business != nil {
		b := s.Business.Redacted(none)
		out.Business = &b
	}
	if s.Coupling != nil {
		c := s.Coupling.Redacted(func(Peer) bool { return false }, none)
		out.Coupling = &c
	}
	if len(s.Dependencies) > 0 {
		deps := slices.Clone(s.Dependencies)
		for i, d := range deps {
			if d.Kind == DependencyCall && d.TargetApplicationID != "" && d.TargetApplicationID != applicationID {
				deps[i].TargetApplicationID, deps[i].TargetApplication, deps[i].TargetWithheld = "", "", true
			}
		}
		out.Dependencies = deps
	}
	if s.Strategy != nil {
		st := *s.Strategy
		withheld := func(o *Owner) *Owner {
			if o == nil {
				return nil
			}
			return &Owner{Withheld: true}
		}
		st.Capabilities = slices.Clone(s.Strategy.Capabilities)
		for i := range st.Capabilities {
			st.Capabilities[i].Owner = withheld(st.Capabilities[i].Owner)
		}
		st.ValueStreams = slices.Clone(s.Strategy.ValueStreams)
		for i := range st.ValueStreams {
			st.ValueStreams[i].Owner = withheld(st.ValueStreams[i].Owner)
		}
		out.Strategy = &st
	}
	return out
}
