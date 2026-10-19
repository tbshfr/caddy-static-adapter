package staticadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	h.loadFn = func(p string) any { loads++; return load(p) }

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
