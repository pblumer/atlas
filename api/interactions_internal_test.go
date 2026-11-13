package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/interaction"
)

// TestTheGraphReadsWatchesAndProductActionsFromMessageSources: the senders that are not
// processes reach the graph as message-sources lists them, and the process rows of that
// listing do not, because the graph reads processes from the definitions themselves.
func TestTheGraphReadsWatchesAndProductActionsFromMessageSources(t *testing.T) {
	got := interactionSources([]messageSourceView{
		{MessageName: "Lieferung", SourceKind: sourceInboundWatch, Enabled: true, ConnectorID: "c1", ConnectorName: "Lager", Kind: "jira"},
		{MessageName: "Bestellt", SourceKind: sourceProductAction, ProductID: "p1", ProductName: "Laptop", Action: "order"},
		{MessageName: "Bestellt", SourceKind: sourceWaitingProcess, ProcessID: "order"},
	})
	want := []interaction.Source{
		{Kind: interaction.KindInboundWatch, Message: "Lieferung", Enabled: true, ConnectorID: "c1", ConnectorName: "Lager", WorkerKind: "jira"},
		{Kind: interaction.KindProductAction, Message: "Bestellt", ProductID: "p1", ProductName: "Laptop", Action: "order"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("sources = %+v, want %+v", got, want)
	}
}

// TestAGraphErrorIsAnswered: an unknown process is 404, any other error 500, and no
// error answers nothing, so the route writes its graph.
func TestAGraphErrorIsAnswered(t *testing.T) {
	for _, tc := range []struct {
		err  error
		ok   bool
		code int
	}{
		{nil, true, http.StatusOK},
		{interaction.ErrUnknownProcess, false, http.StatusNotFound},
		{errors.New("disk"), false, http.StatusInternalServerError},
	} {
		w := httptest.NewRecorder()
		if ok := interactionAnswered(w, "order", tc.err); ok != tc.ok || w.Code != tc.code {
			t.Errorf("interactionAnswered(%v) = %v, %d; want %v, %d", tc.err, ok, w.Code, tc.ok, tc.code)
		}
	}
}
