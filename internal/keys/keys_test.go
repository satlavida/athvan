package keys

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "keys.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	k, secret, err := s.Create("Priya", "phone")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "athv_") || !strings.HasPrefix(secret, k.Prefix) {
		t.Fatalf("secret %q / prefix %q", secret, k.Prefix)
	}

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret written to disk in clear text")
	}

	for range 3 {
		if _, ok := s.Verify(secret); !ok {
			t.Fatal("valid key rejected")
		}
	}
	if _, ok := s.Verify(secret + "x"); ok {
		t.Fatal("wrong key accepted")
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.List()
	if len(got) != 1 || got[0].Uses != 3 || got[0].LastUsedAt == nil || len(got[0].Daily) != 1 {
		t.Fatalf("after reload: %+v", got)
	}

	if err := s2.SetRevoked(k.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Verify(secret); ok {
		t.Fatal("revoked key accepted")
	}
	if err := s2.SetRevoked(k.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Verify(secret); !ok {
		t.Fatal("restored key rejected")
	}
	if err := s2.Delete(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Verify(secret); ok {
		t.Fatal("deleted key accepted")
	}
	if err := s2.Delete(k.ID); err != ErrNotFound {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/wallpaper?key=q", nil)
	if got := FromRequest(r); got != "q" {
		t.Errorf("query: %q", got)
	}
	r.Header.Set("X-API-Key", "h")
	if got := FromRequest(r); got != "h" {
		t.Errorf("header: %q", got)
	}
	r.Header.Set("Authorization", "Bearer b")
	if got := FromRequest(r); got != "b" {
		t.Errorf("bearer: %q", got)
	}
}
