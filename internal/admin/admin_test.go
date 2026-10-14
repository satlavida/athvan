package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/satlavida/athvan/internal/keys"
)

func setup(t *testing.T) (*Server, *keys.Store, *http.ServeMux) {
	t.Helper()
	store, err := keys.Open(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(store, "admin", "s3cret")
	mux := http.NewServeMux()
	s.Register(mux)
	return s, store, mux
}

func do(mux *http.ServeMux, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, mux *http.ServeMux) *http.Cookie {
	t.Helper()
	w := do(mux, "POST", "/admin/api/login", `{"username":"admin","password":"s3cret"}`, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Error("session cookie must be HttpOnly and SameSite=Strict")
			}
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestAdminFlow(t *testing.T) {
	s, store, mux := setup(t)
	if w := do(mux, "GET", "/admin/api/keys", "", nil); w.Code != 401 {
		t.Fatalf("unauthenticated list: %d", w.Code)
	}
	c := login(t, mux)

	w := do(mux, "POST", "/admin/api/keys", `{"name":"Priya","note":"iPhone"}`, c)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created struct {
		Key    struct{ ID string }
		Secret string
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if _, ok := store.Verify(created.Secret); !ok {
		t.Fatal("created key does not verify")
	}

	w = do(mux, "GET", "/admin/api/keys", "", c)
	if strings.Contains(w.Body.String(), `"hash"`) || strings.Contains(w.Body.String(), created.Secret) {
		t.Fatal("list leaks hash or secret")
	}

	if w := do(mux, "POST", "/admin/api/keys/"+created.Key.ID+"/revoke", "", c); w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if _, ok := store.Verify(created.Secret); ok {
		t.Fatal("revoked key verifies")
	}
	if w := do(mux, "DELETE", "/admin/api/keys/"+created.Key.ID, "", c); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	if !s.IsAdmin(r) {
		t.Fatal("session not recognised")
	}
	do(mux, "POST", "/admin/api/logout", "", c)
	if s.IsAdmin(r) {
		t.Fatal("session survives logout")
	}
}

func TestCrossOriginRefused(t *testing.T) {
	_, _, mux := setup(t)
	c := login(t, mux)
	r := httptest.NewRequest("POST", "/admin/api/keys", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin create: %d", w.Code)
	}
}

func TestLoginLockout(t *testing.T) {
	_, _, mux := setup(t)
	for range maxFailures {
		if w := do(mux, "POST", "/admin/api/login", `{"username":"admin","password":"nope"}`, nil); w.Code != 401 {
			t.Fatalf("bad password: %d", w.Code)
		}
	}
	w := do(mux, "POST", "/admin/api/login", `{"username":"admin","password":"s3cret"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after lockout, correct password: %d", w.Code)
	}
}

func TestDisabled(t *testing.T) {
	store, _ := keys.Open(filepath.Join(t.TempDir(), "keys.json"))
	s := New(store, "", "")
	mux := http.NewServeMux()
	s.Register(mux)
	if w := do(mux, "POST", "/admin/api/login", `{"username":"","password":""}`, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled login: %d", w.Code)
	}
}
