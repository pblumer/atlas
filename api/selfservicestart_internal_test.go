package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

func TestPageStartableOrdersTotallyAndCaps(t *testing.T) {
	items := []startableProcess{
		{Key: 9, ProcessID: "b", Name: "Inspect", ApplicationID: "app2", ApplicationName: "field operations"},
		{Key: 3, ProcessID: "z", Name: "audit", ApplicationID: "app1", ApplicationName: "Back Office"},
		{Key: 7, ProcessID: "a", Name: "Inspect", ApplicationID: "app2", ApplicationName: "Field Operations"},
		{Key: 5, ProcessID: "c", Name: "Approve", ApplicationID: "app3", ApplicationName: "Field Operations"},
		{Key: 1, ProcessID: "a", Name: "Inspect", ApplicationID: "app2", ApplicationName: "Field Operations"},
	}
	page, total, truncated := pageStartable(items, 4)
	if total != 5 || !truncated || len(page) != 4 {
		t.Fatalf("total=%d truncated=%v len=%d", total, truncated, len(page))
	}
	got := []uint64{}
	for _, it := range page {
		got = append(got, it.Key)
	}
	// Back Office first; within Field Operations app2 before app3 (ids break the tie
	// between equal names); within app2 by name, then process id, then key.
	want := []uint64{3, 1, 7, 9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if page, total, truncated := pageStartable(nil, 4); len(page) != 0 || total != 0 || truncated {
		t.Fatalf("empty = %v %d %v", page, total, truncated)
	}
}

// TestSelfServicePrincipalRefusesWhatIsNotAPersonsSession pins the gate's three
// refusals at the function, so a later route that reuses it inherits them.
func TestSelfServicePrincipalRefusesWhatIsNotAPersonsSession(t *testing.T) {
	cases := []struct {
		name string
		auth bool
		p    *httpapi.Principal
		code int
	}{
		{"open server", false, nil, http.StatusNotFound},
		{"nobody", true, nil, http.StatusUnauthorized},
		{"machine credential with the user role", true, &httpapi.Principal{UserID: "usr_1", Roles: []string{RoleUser}}, http.StatusForbidden},
		{"session without an id", true, &httpapi.Principal{UserID: " ", Roles: []string{RoleUser}, Session: true}, http.StatusForbidden},
		{"a person's session", true, &httpapi.Principal{UserID: "usr_1", Roles: []string{RoleUser}, Session: true}, 0},
	}
	for _, c := range cases {
		s := &Server{authEnabled: c.auth}
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me/startable-processes", nil)
		if c.p != nil {
			r = r.WithContext(httpapi.WithPrincipal(r.Context(), c.p))
		}
		w := httptest.NewRecorder()
		_, ok := s.selfServicePrincipal(w, r)
		switch {
		case c.code == 0 && (!ok || w.Body.Len() != 0):
			t.Errorf("%s: refused (%d %s)", c.name, w.Code, w.Body.String())
		case c.code != 0 && (ok || w.Code != c.code):
			t.Errorf("%s = %v %d, want %d", c.name, ok, w.Code, c.code)
		}
	}
}
