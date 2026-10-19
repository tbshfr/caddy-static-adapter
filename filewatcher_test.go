package staticadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// expireCache forces the next getCached call for every path to re-check the file.
func (fh *fileHandler) expireCache() {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	for _, f := range fh.files {
		f.lastCheck = time.Time{}
	}
}

// cachedData returns the currently cached data for filePath without triggering a reload.
func (fh *fileHandler) cachedData(filePath string) any {
	fh.mu.RLock()
	defer fh.mu.RUnlock()
	if f := fh.files[filePath]; f != nil {
		return f.data
	}
	return nil
}

func TestGetCachedMultipleRootsLoadOnce(t *testing.T) {
	fileA := filepath.Join(t.TempDir(), "_redirects")
	fileB := filepath.Join(t.TempDir(), "_redirects")
	if err := os.WriteFile(fileA, []byte("/x /a 301\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("/x /b 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()
	loads := 0
	load := h.loadFn
	h.loadFn = func(p string) (any, error) { loads++; return load(p) }

	for i := 0; i < 100; i++ {
		file, want := fileA, "/a"
		if i%2 == 1 {
			file, want = fileB, "/b"
		}
		match := h.getCompiledRedirects(file).MatchFirst("/x")
		if match == nil || match.To != want {
			t.Fatalf("request %d: expected redirect to %s, got %+v", i, want, match)
		}
	}

	if loads != 2 {
		t.Errorf("expected each file to be loaded once, got %d loads", loads)
	}
}

func TestGetCachedEvictsWhenFull(t *testing.T) {
	h := newTestRedirectHandler()
	defer h.Cleanup()

	base := t.TempDir()
	for i := 0; i <= maxCachedFiles; i++ {
		h.getCached(filepath.Join(base, fmt.Sprint(i), "_redirects"))
	}

	h.mu.RLock()
	n := len(h.files)
	h.mu.RUnlock()
	if n != maxCachedFiles {
		t.Errorf("expected cache to be capped at %d entries, got %d", maxCachedFiles, n)
	}
}

func TestWatcherReloadsEachRoot(t *testing.T) {
	fileA := filepath.Join(t.TempDir(), "_redirects")
	fileB := filepath.Join(t.TempDir(), "_redirects")
	if err := os.WriteFile(fileA, []byte("/x /a 301\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("/x /b 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()
	h.getCached(fileA)
	h.getCached(fileB)

	h.mu.RLock()
	hasWatcher := h.watcher != nil
	h.mu.RUnlock()
	if !hasWatcher {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	if err := os.WriteFile(fileA, []byte("/x /a2 301\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("/x /b2 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := h.cachedData(fileA).(*CompiledRedirects)
		b, _ := h.cachedData(fileB).(*CompiledRedirects)
		if a != nil && b != nil {
			ma, mb := a.MatchFirst("/x"), b.MatchFirst("/x")
			if ma != nil && mb != nil && ma.To == "/a2" && mb.To == "/b2" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out: watcher did not reload both files")
}

// writeWithMtime writes content and sets a distinct mtime so polling sees a change.
func writeWithMtime(t *testing.T, file, content string, offset time.Duration) {
	t.Helper()
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(offset)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestStrictFailureKeepsPreviousRules(t *testing.T) {
	file := filepath.Join(t.TempDir(), "_redirects")
	writeWithMtime(t, file, "/old /new 301\n", 0)

	h := newTestRedirectHandler()
	h.Strict = true
	defer h.Cleanup()

	if h.getCompiledRedirects(file).MatchFirst("/old") == nil {
		t.Fatal("expected initial rules to load")
	}

	writeWithMtime(t, file, "/old /new 301\nbroken\n", 10*time.Second)
	h.expireCache()

	compiled := h.getCompiledRedirects(file)
	if compiled == nil || compiled.MatchFirst("/old") == nil {
		t.Fatal("expected previous rules to stay active after a strict-mode failure")
	}

	writeWithMtime(t, file, "/old /newer 301\n", 20*time.Second)
	h.expireCache()

	match := h.getCompiledRedirects(file).MatchFirst("/old")
	if match == nil || match.To != "/newer" {
		t.Fatalf("expected fixed file to load, got %+v", match)
	}
}

func TestLoadErrorLoggedOnce(t *testing.T) {
	file := filepath.Join(t.TempDir(), "_redirects")
	writeWithMtime(t, file, "broken\n", 0)

	core, logs := observer.New(zap.ErrorLevel)
	h := newTestRedirectHandler()
	h.Strict = true
	h.logger = zap.New(core)
	defer h.Cleanup()

	for i := 0; i < 5; i++ {
		h.expireCache()
		if h.getCompiledRedirects(file) != nil {
			t.Fatal("expected no rules for a file that fails strict mode on first load")
		}
	}
	if n := logs.Len(); n != 1 {
		t.Fatalf("expected the persisting error to be logged once, got %d entries", n)
	}

	// A different error is logged again.
	writeWithMtime(t, file, "/ok /fine 301\nstill broken\n", 10*time.Second)
	h.expireCache()
	h.getCompiledRedirects(file)
	if n := logs.Len(); n != 2 {
		t.Fatalf("expected a new error to be logged, got %d entries", n)
	}

	// After a successful load the same error is reported again if it comes back.
	writeWithMtime(t, file, "/ok /fine 301\n", 20*time.Second)
	h.expireCache()
	h.getCompiledRedirects(file)
	writeWithMtime(t, file, "/ok /fine 301\nstill broken\n", 30*time.Second)
	h.expireCache()
	h.getCompiledRedirects(file)
	if n := logs.Len(); n != 3 {
		t.Fatalf("expected recurring error to be logged again after recovery, got %d entries", n)
	}
}

func TestUnreadableFileRetriedWithoutMtimeChange(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of permissions")
	}
	file := filepath.Join(t.TempDir(), "_redirects")
	writeWithMtime(t, file, "/old /new 301\n", 0)
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()

	if h.getCompiledRedirects(file) != nil {
		t.Fatal("expected no rules while the file is unreadable")
	}

	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	h.expireCache()

	compiled := h.getCompiledRedirects(file)
	if compiled == nil || compiled.MatchFirst("/old") == nil {
		t.Fatal("expected rules to load once the file is readable again")
	}
}
