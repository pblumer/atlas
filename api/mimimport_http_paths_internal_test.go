package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Importing a MIM/FIM workflow as a draft, when the body cannot be read, the
// target application cannot be read or may not be written into, what the import
// would replace cannot be read, or the draft cannot be saved. Each is refused with
// no draft written.

// mimImportHTTPPathsXOML is one small sequential workflow.
const mimImportHTTPPathsXOML = `<SequentialWorkflow>
  <ApprovalActivity Description="Manager approval"/>
  <UpdateResourceActivity Description="Write back"/>
</SequentialWorkflow>`

// mimImportHTTPPathsPost imports the workflow under a name, with an optional query.
func mimImportHTTPPathsPost(t *testing.T, s *Server, query string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/imports/mim?name=Onboarding"+query,
		strings.NewReader(mimImportHTTPPathsXOML))
}

// mimImportHTTPPathsProcessID imports once on a scratch server to learn the
// process id the converter derives from the name.
func mimImportHTTPPathsProcessID(t *testing.T) string {
	t.Helper()
	code, body := mimImportHTTPPathsPost(t, newServerForErrors(t), "")
	if code != http.StatusOK {
		t.Fatalf("import: %d %s", code, body)
	}
	var res struct {
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil || res.ProcessID == "" {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	return res.ProcessID
}

// mimImportHTTPPathsDrafts counts the drafts saved on a server.
func mimImportHTTPPathsDrafts(t *testing.T, s *Server) int {
	t.Helper()
	var (
		n   int
		err error
	)
	s.do(func() {
		var all []draft
		all, err = s.drafts.LoadAll()
		n = len(all)
	})
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	return n
}

// TestMimImportHTTPRefusesAnUnreadableBody before converting anything.
func TestMimImportHTTPRefusesAnUnreadableBody(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/imports/mim", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("import = %d %s, want 400", code, body)
	}
}

// TestMimImportHTTPTheTargetApplicationDecides. An application that cannot be read
// is a fault, and a protected system application takes no import — the content of
// one is platform-managed, and an import must not be a way around that.
func TestMimImportHTTPTheTargetApplicationDecides(t *testing.T) {
	srv := newServerForErrors(t)
	var err error
	srv.do(func() {
		if err = srv.projects.Save(project{ID: "sys", Name: "System", Protected: true}); err != nil {
			return
		}
		err = srv.projects.Save(project{ID: "torn", Name: "Torn"})
	})
	if err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	if err := os.WriteFile(srv.projects.FileFor("torn"), []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt project: %v", err)
	}

	if code, body := mimImportHTTPPathsPost(t, srv, "&projectId=sys"); code != http.StatusForbidden ||
		!strings.Contains(body, "protected system project") {
		t.Errorf("into a protected application = %d %s, want 403", code, body)
	}
	if code, body := mimImportHTTPPathsPost(t, srv, "&projectId=torn"); code != http.StatusInternalServerError ||
		!strings.Contains(body, "read project") {
		t.Errorf("into an unreadable application = %d %s, want 500", code, body)
	}
	if n := mimImportHTTPPathsDrafts(t, srv); n != 0 {
		t.Errorf("%d draft(s) written by refused imports", n)
	}
}

// TestMimImportHTTPADraftThatCannotBeReadOrWrittenIsAFault. The import first reads
// what it would replace and then writes; when either fails the caller is told,
// and nothing is replaced.
func TestMimImportHTTPADraftThatCannotBeReadOrWrittenIsAFault(t *testing.T) {
	pid := mimImportHTTPPathsProcessID(t)

	t.Run("the draft it would replace cannot be read", func(t *testing.T) {
		srv := newServerForErrors(t)
		if err := os.WriteFile(srv.drafts.FileFor(pid), []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt draft: %v", err)
		}
		if code, body := mimImportHTTPPathsPost(t, srv, ""); code != http.StatusInternalServerError ||
			!strings.Contains(body, "read what the import would land on") {
			t.Errorf("import = %d %s, want 500", code, body)
		}
	})

	t.Run("the draft cannot be written", func(t *testing.T) {
		srv := newServerForErrors(t)
		reconcileApplyPathsBlockSave(t, srv.drafts.FileFor(pid))
		if code, body := mimImportHTTPPathsPost(t, srv, ""); code != http.StatusInternalServerError ||
			!strings.Contains(body, "save draft") {
			t.Errorf("import = %d %s, want 500", code, body)
		}
		if n := mimImportHTTPPathsDrafts(t, srv); n != 0 {
			t.Errorf("%d draft(s) written by a failed import", n)
		}
	})
}
