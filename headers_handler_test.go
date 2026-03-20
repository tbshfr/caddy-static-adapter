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

// newTestHeadersHandler creates a Handler with test defaults (no Caddy context).
func newTestHeadersHandler() *Handler {
	h := &Handler{}
	h.logger = zap.NewNop()
	h.logPrefix = "static_headers"
	h.fileName = "_headers"
	h.loadFn = h.loadFile
	return h
}

func TestGetCompiledLoadsFile(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	compiled := h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules, got nil")
	}

	ops := compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "value1" {
		t.Fatalf("expected 1 op with value1, got %v", ops)
	}
}

func TestGetCompiledReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// Initial load.
	compiled := h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules")
	}
	ops := compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "value1" {
		t.Fatalf("expected value1, got %v", ops)
	}

	// Update the file with new content and a future mod time.
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(headersFile, future, future); err != nil {
		t.Fatal(err)
	}

	// Reset lastCheck to force re-check.
	h.mu.Lock()
	h.lastCheck = time.Time{}
	h.mu.Unlock()

	// Should pick up the new file.
	compiled = h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules after reload")
	}
	ops = compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "value2" {
		t.Fatalf("expected value2 after reload, got %v", ops)
	}
}

func TestGetCompiledMissingFile(t *testing.T) {
	h := newTestHeadersHandler()
	defer h.Cleanup()

	compiled := h.getCompiled("/nonexistent/path/_headers")
	if compiled != nil {
		t.Fatal("expected nil for missing file")
	}
}

func TestGetCompiledFileAppears(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// File doesn't exist yet.
	compiled := h.getCompiled(headersFile)
	if compiled != nil {
		t.Fatal("expected nil for missing file")
	}

	// Create the file.
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: appeared\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Reset lastCheck to force re-check.
	h.mu.Lock()
	h.lastCheck = time.Time{}
	h.mu.Unlock()

	// Should pick it up now.
	compiled = h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules after file appeared")
	}
	ops := compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "appeared" {
		t.Fatalf("expected 'appeared', got %v", ops)
	}
}

func TestGetCompiledCachesWithinInterval(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: cached\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// Initial load.
	compiled1 := h.getCompiled(headersFile)
	if compiled1 == nil {
		t.Fatal("expected compiled rules")
	}

	// Second call within check interval should return same pointer.
	compiled2 := h.getCompiled(headersFile)
	if compiled1 != compiled2 {
		t.Fatal("expected same compiled pointer within check interval")
	}
}

func TestGetCompiledDifferentFilesSameModTime(t *testing.T) {
	// Create two separate _headers files with different contents but identical mod times.
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	headersFile1 := filepath.Join(dir1, "_headers")
	headersFile2 := filepath.Join(dir2, "_headers")

	if err := os.WriteFile(headersFile1, []byte("/page\n  X-Test: file1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(headersFile2, []byte("/page\n  X-Test: file2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Force identical modification times on both files.
	now := time.Now()
	if err := os.Chtimes(headersFile1, now, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(headersFile2, now, now); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// First, compile headers from the first file.
	compiled1 := h.getCompiled(headersFile1)
	if compiled1 == nil {
		t.Fatal("expected compiled rules for first file")
	}
	ops1 := compiled1.MatchOrdered("/page", "")
	if len(ops1) != 1 || ops1[0].Value != "file1" {
		t.Fatalf("expected 'file1' headers, got %v", ops1)
	}

	// Reset lastCheck to force a re-check before switching to the second file.
	h.mu.Lock()
	h.lastCheck = time.Time{}
	h.mu.Unlock()

	// Now compile headers from the second file and ensure we don't get the first file's rules.
	compiled2 := h.getCompiled(headersFile2)
	if compiled2 == nil {
		t.Fatal("expected compiled rules for second file")
	}
	ops2 := compiled2.MatchOrdered("/page", "")
	if len(ops2) != 1 || ops2[0].Value != "file2" {
		t.Fatalf("expected 'file2' headers, got %v", ops2)
	}
}

func TestGetCompiledReloadsViaWatcher(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// Initial load — also sets up the filesystem watcher.
	compiled := h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules")
	}

	h.mu.RLock()
	hasWatcher := h.watcher != nil
	h.mu.RUnlock()
	if !hasWatcher {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	ops := compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "value1" {
		t.Fatalf("expected value1, got %v", ops)
	}

	// Modify the file. The watcher should detect this without
	// needing to wait for the polling interval.
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Poll until the watcher picks up the change or timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		compiled = h.getCompiled(headersFile)
		if compiled != nil {
			ops = compiled.MatchOrdered("/page", "")
			if len(ops) == 1 && ops[0].Value == "value2" {
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watcher-triggered reload to value2")
}

func TestGetCompiledFileAppearsViaWatcher(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// File doesn't exist yet — watcher is set up on the directory.
	compiled := h.getCompiled(headersFile)
	if compiled != nil {
		t.Fatal("expected nil for missing file")
	}

	h.mu.RLock()
	hasWatcher := h.watcher != nil
	h.mu.RUnlock()
	if !hasWatcher {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	// Create the file. The watcher should detect the new file.
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: appeared\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Poll until the watcher picks up the new file or timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		compiled = h.getCompiled(headersFile)
		if compiled != nil {
			ops := compiled.MatchOrdered("/page", "")
			if len(ops) == 1 && ops[0].Value == "appeared" {
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watcher to detect new _headers file")
}

func TestWatcherProactiveReload(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// Initial load — sets up the filesystem watcher.
	compiled := h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules")
	}

	h.mu.RLock()
	hasWatcher := h.watcher != nil
	h.mu.RUnlock()
	if !hasWatcher {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	ops := compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "value1" {
		t.Fatalf("expected value1, got %v", ops)
	}

	// Modify the file.
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to proactively reload — no getCompiled call.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.RLock()
		c, _ := h.cached.(*Compiled)
		h.mu.RUnlock()
		if c != nil {
			ops := c.MatchOrdered("/page", "")
			if len(ops) == 1 && ops[0].Value == "value2" {
				return // success: file was proactively reloaded
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out: watcher did not proactively reload the file")
}

func TestWatcherDebounce(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()

	// Initial load — sets up the filesystem watcher.
	compiled := h.getCompiled(headersFile)
	if compiled == nil {
		t.Fatal("expected compiled rules")
	}

	h.mu.RLock()
	hasWatcher := h.watcher != nil
	h.mu.RUnlock()
	if !hasWatcher {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	ops := compiled.MatchOrdered("/page", "")
	if len(ops) != 1 || ops[0].Value != "value1" {
		t.Fatalf("expected value1, got %v", ops)
	}

	// Simulate an editor save: truncate then write.
	// This produces two rapid fsnotify events. Without debouncing,
	// the watcher would reload the empty file (0 rules) first.
	if err := os.WriteFile(headersFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	// Immediately write the real content (< debounceDelay later).
	if err := os.WriteFile(headersFile, []byte("/page\n  X-Test: value2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + reload to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.RLock()
		c, _ := h.cached.(*Compiled)
		h.mu.RUnlock()
		if c != nil {
			ops := c.MatchOrdered("/page", "")
			if len(ops) == 1 && ops[0].Value == "value2" {
				return // success: got final value, no intermediate 0-rule state served
			}
			// If we see 0 rules, the debounce failed to coalesce.
			if c.totalRules() == 0 {
				t.Fatal("debounce failed: saw intermediate empty state (0 rules)")
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out: watcher did not reload after debounced editor save")
}

func TestServeHTTPBlocks_headers(t *testing.T) {
	h := newTestHeadersHandler()
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
		{"/_headers", true},
		{"//_headers", true},            // multiple leading slashes
		{"///_headers", true},           // multiple leading slashes
		{"/foo/../_headers", true},      // path traversal
		{"/_headers/../_headers", true}, // path traversal
		{"/_headers/", true},            // trailing slash normalization
		{"/./_headers", true},           // dot segment normalization
		{"/_headers/.", true},           // terminal dot segment
		{"/page", false},
		{"/_redirects", false}, // not this handler's concern
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
