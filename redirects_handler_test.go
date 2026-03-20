package staticadapter

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

// newTestRedirectHandler creates a RedirectHandler with test defaults (no Caddy context).
func newTestRedirectHandler() *RedirectHandler {
	h := &RedirectHandler{}
	h.logger = zap.NewNop()
	h.logPrefix = "static_redirects"
	h.fileName = "_redirects"
	h.loadFn = h.loadRedirectFile
	return h
}

func TestRedirectServeHTTP(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte("/old /new 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	rules := h.getCompiledRedirects(redirectsFile)
	if rules == nil {
		t.Fatal("expected compiled redirect rules, got nil")
	}
	if rules.totalRules() != 1 {
		t.Fatalf("expected 1 rule, got %d", rules.totalRules())
	}

	// Create a test request matching the redirect.
	req := httptest.NewRequest("GET", "/old", nil)
	rec := httptest.NewRecorder()

	// Simulate what ServeHTTP does (without the Caddy replacer).
	match := rules.MatchFirst("/old")
	if match == nil {
		t.Fatal("expected redirect match for /old")
	}
	to := expandRedirectTo(match.pathPattern, match.To, normalizePath(req.URL.Path))
	http.Redirect(rec, req, to, match.Status)

	if rec.Code != 301 {
		t.Errorf("expected status 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/new" {
		t.Errorf("expected Location /new, got %s", loc)
	}
}

func TestRedirectServeHTTPNoMatch(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte("/old /new 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	rules := h.getCompiledRedirects(redirectsFile)
	if rules == nil {
		t.Fatal("expected compiled redirect rules, got nil")
	}

	// Request that doesn't match.
	match := rules.MatchFirst("/other")
	if match != nil {
		t.Fatal("expected no redirect match for /other")
	}
}

// ---------- getCompiledRedirects / Reload Tests ----------

func TestGetCompiledRedirectsLoadsFile(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte("/old /new 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	rules := h.getCompiledRedirects(redirectsFile)
	if rules == nil {
		t.Fatal("expected compiled redirect rules, got nil")
	}
	if rules.totalRules() != 1 {
		t.Fatalf("expected 1 rule, got %d", rules.totalRules())
	}
}

func TestGetCompiledRedirectsReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte("/old /new1 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	// Initial load.
	rules := h.getCompiledRedirects(redirectsFile)
	if rules == nil || rules.totalRules() != 1 {
		t.Fatalf("unexpected initial rules: %v", rules)
	}
	match := rules.MatchFirst("/old")
	if match == nil || match.To != "/new1" {
		t.Fatalf("expected initial redirect to /new1")
	}

	// Update the file.
	if err := os.WriteFile(redirectsFile, []byte("/old /new2 302\n"), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(redirectsFile, future, future); err != nil {
		t.Fatal(err)
	}

	// Reset lastCheck to force re-check.
	h.mu.Lock()
	h.lastCheck = time.Time{}
	h.mu.Unlock()

	rules = h.getCompiledRedirects(redirectsFile)
	if rules == nil || rules.totalRules() != 1 {
		t.Fatalf("expected 1 rule after reload, got %v", rules)
	}
	match = rules.MatchFirst("/old")
	if match == nil || match.To != "/new2" {
		t.Fatalf("expected /new2 after reload")
	}
}

func TestGetCompiledRedirectsMissingFile(t *testing.T) {
	h := newTestRedirectHandler()
	defer h.Cleanup()

	rules := h.getCompiledRedirects("/nonexistent/path/_redirects")
	if rules != nil {
		t.Fatal("expected nil for missing file")
	}
}

func TestGetCompiledRedirectsFileAppears(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")

	h := newTestRedirectHandler()
	defer h.Cleanup()

	// File doesn't exist yet.
	rules := h.getCompiledRedirects(redirectsFile)
	if rules != nil {
		t.Fatal("expected nil for missing file")
	}

	// Create the file.
	if err := os.WriteFile(redirectsFile, []byte("/old /new 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Reset lastCheck to force re-check.
	h.mu.Lock()
	h.lastCheck = time.Time{}
	h.mu.Unlock()

	rules = h.getCompiledRedirects(redirectsFile)
	if rules == nil || rules.totalRules() != 1 {
		t.Fatalf("expected 1 rule after file appeared, got %v", rules)
	}
}

func TestGetCompiledRedirectsCachesWithinInterval(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte("/old /new 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	// Initial load.
	rules1 := h.getCompiledRedirects(redirectsFile)
	if rules1 == nil {
		t.Fatal("expected compiled redirect rules")
	}

	// Second call within interval should return same data.
	rules2 := h.getCompiledRedirects(redirectsFile)
	if rules1.totalRules() != rules2.totalRules() {
		t.Fatal("expected same rules within check interval")
	}
}

func TestGetCompiledRedirectsReloadsViaWatcher(t *testing.T) {
	dir := t.TempDir()
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte("/old /new1 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	// Initial load — sets up the filesystem watcher.
	rules := h.getCompiledRedirects(redirectsFile)
	if rules == nil {
		t.Fatal("expected compiled redirect rules")
	}

	h.mu.RLock()
	hasWatcher := h.watcher != nil
	h.mu.RUnlock()
	if !hasWatcher {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	// Modify the file.
	if err := os.WriteFile(redirectsFile, []byte("/old /new2 302\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Poll until the watcher picks up the change.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rules = h.getCompiledRedirects(redirectsFile)
		if rules != nil {
			match := rules.MatchFirst("/old")
			if match != nil && match.To == "/new2" {
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watcher-triggered reload")
}

func TestRedirectStrictMode(t *testing.T) {
	dir := t.TempDir()
	// Mix valid and invalid lines.
	content := "/old /new 301\n/bad 999\n/foo /bar\n"
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(redirectsFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := &RedirectHandler{}
	h.logger = zap.NewNop()
	h.logPrefix = "static_redirects"
	h.fileName = "_redirects"
	h.Strict = true
	h.loadFn = h.loadRedirectFile
	defer h.Cleanup()

	rules := h.getCompiledRedirects(redirectsFile)
	if rules != nil {
		t.Fatal("expected nil rules in strict mode with parse errors")
	}
}

func TestServeHTTPBlocks_redirects(t *testing.T) {
	h := newTestRedirectHandler()
	defer h.Cleanup()

	nextCalled := false
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		nextCalled = true
		return nil
	})

	tests := []struct {
		path      string
		wantBlock bool
	}{
		{"/_redirects", true},
		{"//_redirects", true},              // multiple leading slashes
		{"///_redirects", true},             // multiple leading slashes
		{"/foo/../_redirects", true},        // path traversal
		{"/_redirects/../_redirects", true}, // path traversal
		{"/_redirects/", true},              // trailing slash normalization
		{"/./_redirects", true},             // dot segment normalization
		{"/_redirects/.", true},             // terminal dot segment
		{"/page", false},
		{"/_headers", false}, // not this handler's concern
		{"/", false},
	}

	for _, tt := range tests {
		nextCalled = false
		req := httptest.NewRequest("GET", tt.path, nil)
		rec := httptest.NewRecorder()

		err := h.ServeHTTP(rec, req, next)

		if tt.wantBlock {
			if err == nil {
				t.Errorf("path %q: expected error, got nil", tt.path)
				continue
			}
			httpErr, ok := err.(caddyhttp.HandlerError)
			if !ok {
				t.Errorf("path %q: expected caddyhttp.HandlerError, got %T", tt.path, err)
				continue
			}
			if httpErr.StatusCode != http.StatusNotFound {
				t.Errorf("path %q: expected status 404, got %d", tt.path, httpErr.StatusCode)
			}
			if nextCalled {
				t.Errorf("path %q: next handler should not be called", tt.path)
			}
		} else {
			// Without a Caddy replacer, ServeHTTP falls through to next.
			if !nextCalled {
				t.Errorf("path %q: expected next handler to be called", tt.path)
			}
		}
	}
}
