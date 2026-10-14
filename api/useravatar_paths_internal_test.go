package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An account's picture when the files beside the account cannot be read, written
// or removed, and the requests refused before anything is touched. In each failed
// write the account record must not claim a picture it does not have.

// userAvatarPathsPNG passes the format check: the PNG signature and some bytes.
const userAvatarPathsPNG = "\x89PNG\r\n\x1a\n" + "a face"

// userAvatarPathsAccount files the account a picture belongs to.
func userAvatarPathsAccount(t *testing.T, s *Server) {
	t.Helper()
	pendingWorkPathsAccount(t, s, User{ID: "usr_ada", Username: "ada"})
}

// userAvatarPathsSource reads the provenance the account record carries.
func userAvatarPathsSource(t *testing.T, s *Server) string {
	t.Helper()
	var (
		u   User
		ok  bool
		err error
	)
	s.do(func() { u, ok, err = s.users.Get("usr_ada") })
	if err != nil || !ok {
		t.Fatalf("read account: ok=%v err=%v", ok, err)
	}
	return u.AvatarSource
}

// userAvatarPathsObstruct puts a non-empty directory where a picture file goes,
// so reading it, removing it or replacing it fails as a full or broken disk would.
func userAvatarPathsObstruct(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("obstruct %s: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("obstruct %s: %v", path, err)
	}
}

// userAvatarPathsPut uploads a PNG.
func userAvatarPathsPut(t *testing.T, s *Server) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/usr_ada/avatar", strings.NewReader(userAvatarPathsPNG))
	req.Header.Set("Content-Type", "image/png")
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestUserAvatarAPictureThatCannotBeReadIsAFault, and not "no picture": the
// account may well have one.
func TestUserAvatarAPictureThatCannotBeReadIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	userAvatarPathsAccount(t, srv)
	userAvatarPathsObstruct(t, srv.users.avatarPath("usr_ada", "jpg"))

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/users/usr_ada/avatar", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "read picture") {
		t.Errorf("get = %d %s, want 500", code, body)
	}
}

// TestUserAvatarAFailedUploadClaimsNothing. When the picture cannot be written,
// or the previous format cannot be cleared away, the upload fails and the account
// does not say it carries an uploaded picture.
func TestUserAvatarAFailedUploadClaimsNothing(t *testing.T) {
	t.Run("the picture cannot be written", func(t *testing.T) {
		srv := newServerForErrors(t)
		userAvatarPathsAccount(t, srv)
		reconcileApplyPathsBlockSave(t, srv.users.avatarPath("usr_ada", "png"))

		if code, body := userAvatarPathsPut(t, srv); code != http.StatusInternalServerError ||
			!strings.Contains(body, "save picture") {
			t.Errorf("put = %d %s, want 500", code, body)
		}
		if got := userAvatarPathsSource(t, srv); got != "" {
			t.Errorf("avatarSource = %q, want none", got)
		}
	})

	t.Run("the previous format cannot be removed", func(t *testing.T) {
		srv := newServerForErrors(t)
		userAvatarPathsAccount(t, srv)
		userAvatarPathsObstruct(t, srv.users.avatarPath("usr_ada", "jpg"))

		if code, body := userAvatarPathsPut(t, srv); code != http.StatusInternalServerError ||
			!strings.Contains(body, "remove stale avatar") {
			t.Errorf("put = %d %s, want 500 naming the stale picture", code, body)
		}
		if got := userAvatarPathsSource(t, srv); got != "" {
			t.Errorf("avatarSource = %q, want none", got)
		}
	})
}

// TestUserAvatarAFailedRemovalKeepsTheRecord. A picture that could not be removed
// is still there, and the account keeps saying where it came from.
func TestUserAvatarAFailedRemovalKeepsTheRecord(t *testing.T) {
	srv := newServerForErrors(t)
	userAvatarPathsAccount(t, srv)
	if code, body := userAvatarPathsPut(t, srv); code != http.StatusNoContent {
		t.Fatalf("put = %d %s", code, body)
	}
	userAvatarPathsObstruct(t, srv.users.avatarPath("usr_ada", "jpg"))

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodDelete, "/api/v1/users/usr_ada/avatar", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "remove picture") {
		t.Errorf("delete = %d %s, want 500", code, body)
	}
	if got := userAvatarPathsSource(t, srv); got != AvatarUploaded {
		t.Errorf("avatarSource = %q, want it still %q", got, AvatarUploaded)
	}
}

// TestUserAvatarRefusalsBeforeTheStore. A body that cannot be read, a request that
// carries nobody behind the access boundary, and a type the store does not keep
// are each refused without touching the account.
func TestUserAvatarRefusalsBeforeTheStore(t *testing.T) {
	srv := newServerForErrors(t)
	userAvatarPathsAccount(t, srv)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/usr_ada/avatar", errReader{})
	req.Header.Set("Content-Type", "image/png")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Errorf("unreadable = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if err := srv.users.saveAvatar("usr_ada", []byte("GIF89a"), "image/gif"); err == nil ||
		!strings.Contains(err.Error(), "unsupported picture type") {
		t.Errorf("saveAvatar(gif) = %v, want the type refused", err)
	}

	authed := newServerWithOptions(t, WithAuth())
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/usr_ada/avatar", nil)
	req.SetPathValue("id", "usr_ada")
	authed.handleDeleteAvatar(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("delete by nobody = %d %s, want 403", rec.Code, rec.Body.String())
	}
}
