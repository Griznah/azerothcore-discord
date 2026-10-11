package ntfy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type capture struct {
	method string
	path   string
	body   string
	header http.Header
}

func captureServer(t *testing.T, status int) (*httptest.Server, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.body = string(body)
		cap.header = r.Header.Clone()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func TestPublish(t *testing.T) {
	srv, cap := captureServer(t, http.StatusOK)

	c := New(srv.URL, "tok123")
	if err := c.Publish(context.Background(), "mytopic", "Realm offline", "Realm offline", "high", "warning"); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if cap.method != http.MethodPost {
		t.Errorf("method = %q, want POST", cap.method)
	}
	if cap.path != "/mytopic" {
		t.Errorf("path = %q, want /mytopic", cap.path)
	}
	if cap.body != "Realm offline" {
		t.Errorf("body = %q, want message text", cap.body)
	}
	if got := cap.header.Get("Authorization"); got != "Bearer tok123" {
		t.Errorf("Authorization = %q, want Bearer tok123", got)
	}
	if got := cap.header.Get("Title"); got != "Realm offline" {
		t.Errorf("Title = %q, want %q", got, "Realm offline")
	}
	if got := cap.header.Get("Priority"); got != "high" {
		t.Errorf("Priority = %q, want high", got)
	}
	if got := cap.header.Get("Tags"); got != "warning" {
		t.Errorf("Tags = %q, want warning", got)
	}
}

func TestPublishNoTokenOmitsAuth(t *testing.T) {
	srv, cap := captureServer(t, http.StatusOK)

	c := New(srv.URL, "")
	if err := c.Publish(context.Background(), "t", "title", "msg", "default", ""); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if got := cap.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
	if got := cap.header.Get("Priority"); got != "default" {
		t.Errorf("Priority = %q, want default", got)
	}
	if got := cap.header.Get("Tags"); got != "" {
		t.Errorf("Tags = %q, want empty", got)
	}
}

func TestPublishTrailingSlashServerURL(t *testing.T) {
	srv, cap := captureServer(t, http.StatusOK)

	c := New(srv.URL+"/", "")
	if err := c.Publish(context.Background(), "mytopic", "t", "m", "", ""); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if cap.path != "/mytopic" {
		t.Errorf("path = %q, want single-slash /mytopic", cap.path)
	}
}

func TestPublishNon2xxError(t *testing.T) {
	srv, _ := captureServer(t, http.StatusForbidden)

	c := New(srv.URL, "")
	if err := c.Publish(context.Background(), "mytopic", "t", "m", "", ""); err == nil {
		t.Fatal("expected error on non-2xx response")
	}
}
