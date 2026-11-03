package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/formavailability"
	"github.com/pblumer/atlas/api/httpapi"
)

// What a name field's live availability check may reach (ADR-0519).
//
// Like the form lookups (formlookups.go) it is a query through the broker and nothing
// on the job path: these closures are its whole reach into this server, so the single
// writer (I3) and the Worker store stay in the package that owns them.

// availabilityBurst and availabilityRatePerSecond are the per-person budget. The field
// debounces, so a person typing a name makes a request or two a second at most; the
// budget leaves that untouched and stops a script from walking a tenant's names
// through somebody's order form.
const (
	availabilityBurst         = 10
	availabilityRatePerSecond = 2
)

// newFormAvailability builds the service. It runs after newFormLookups, whose broker
// it shares: one queue per job type, one presence for each Worker Instance.
func (s *Server) newFormAvailability() {
	rate := newRateLimiter(availabilityBurst, availabilityRatePerSecond)
	s.formAvailability = formavailability.New(formavailability.Config{
		Worker: func(name string) (formavailability.Worker, bool, error) {
			wk, ok, err := s.lookupWorker(name)
			return formavailability.Worker{WorkerType: wk.WorkerType, Enabled: wk.Enabled, FormLookups: wk.FormLookups}, ok, err
		},
		Ask:   s.queryBroker.Ask,
		Allow: rate.allow,
		Principal: func(r *http.Request) string {
			if p := httpapi.PrincipalFrom(r.Context()); p != nil {
				return p.UserID
			}
			return ""
		},
		MaxBody: s.budgets().Request,
	})
}

// handleFormFieldAvailability is POST /api/v1/forms/{id}/fields/{field}/availability:
// whether the name typed into an availability field of a stored form is still free.
//
// Whoever may read the form may ask, as for a decision field's options — but only a
// signed-in person: the check asks through a Worker's credential, and an anonymous
// door to that would make a public page an oracle for a tenant's names. A public form
// carrying the field still submits: the check is a courtesy the provisioning process
// repeats, so a form whose field cannot be checked only loses the early warning.
func (s *Server) handleFormFieldAvailability(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled {
		if p := httpapi.PrincipalFrom(r.Context()); p == nil || p.UserID == "" {
			httpapi.Error(w, http.StatusForbidden, "checking a name needs a signed-in person")
			return
		}
	}
	var (
		rec     form
		ok      bool
		loadErr error
	)
	s.do(func() { rec, ok, loadErr = s.forms.Get(r.PathValue("id")) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+loadErr.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no form with that id")
	default:
		s.formAvailability.WriteCheck(w, r, []byte(rec.Schema), r.PathValue("field"))
	}
}
