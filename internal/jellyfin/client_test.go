package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshAndInfo(t *testing.T) {
	var refreshed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.Contains(auth, `Token="secret"`) || r.Header.Get("X-Emby-Token") != "secret" {
			t.Errorf("auth %q", auth)
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = w.Write([]byte(`{"ServerName":"Home","Version":"10.10.0"}`))
		case "/Library/Refresh":
			if r.Method != http.MethodPost {
				t.Errorf("method %s", r.Method)
			}
			refreshed = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "secret")
	name, err := c.Test(context.Background())
	if err != nil || name != "Home 10.10.0" {
		t.Fatalf("%s %v", name, err)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !refreshed {
		t.Fatal("refresh was not called")
	}
}

func TestDisabled(t *testing.T) {
	c := New("", "")
	if c.Enabled() {
		t.Fatal("expected disabled")
	}
	if _, err := c.Test(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
