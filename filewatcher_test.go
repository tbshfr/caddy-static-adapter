package staticadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// expireCache forces the next getCached call for every path to re-check the file.
func (fh *fileHandler) expireCache() {
	if files := fh.files.Load(); files != nil {
		for _, f := range *files {
			f.expire()
		}
	}
}

// expire makes f due for a check and older than any entry checked within
// the last hour.
func (f *cachedFile) expire() {
	f.lastCheck.Store(monotonicNow() - int64(time.Hour))
}

// cachedData returns the currently cached data for filePath without triggering a reload.
func (fh *fileHandler) cachedData(filePath string) any {
	if f := fh.lookup(filePath); f != nil {
		return f.get()
	}
	return nil
}

// hasWatcher reports whether any cached file is watched by the shared watcher.
func (fh *fileHandler) hasWatcher() bool {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if files := fh.files.Load(); files != nil {
		for _, f := range *files {
			if f.watched {
				return true
			}
		}
	}
	return false
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

	n := len(*h.files.Load())
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

	hasWatcher := h.hasWatcher()
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

// pollOnce runs one polling check for file, bypassing the watcher.
func (fh *fileHandler) pollOnce(file string, f *cachedFile) any {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	fh.refreshLocked(file, f, false)
	return f.get()
}

func TestPollingDetectsChangesWithPreservedMtime(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "_redirects")
	mtime := time.Now().Add(-time.Hour)
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	target := func(data any) string {
		t.Helper()
		match := data.(*CompiledRedirects).MatchFirst("/x")
		if match == nil {
			t.Fatal("expected a rule for /x")
		}
		return match.To
	}

	h := newTestRedirectHandler()
	loads := 0
	load := h.loadFn
	h.loadFn = func(p string) (any, error) { loads++; return load(p) }
	f := &cachedFile{}

	write(file, "/x /aaa 301\n")
	if got := target(h.pollOnce(file, f)); got != "/aaa" {
		t.Fatalf("expected /aaa, got %s", got)
	}

	// Unchanged file: no reload.
	h.pollOnce(file, f)
	if loads != 1 {
		t.Fatalf("expected no reload for an unchanged file, got %d loads", loads)
	}

	// Atomic replace with the same size and mtime (rsync -t, Nix).
	tmp := filepath.Join(dir, ".tmp")
	write(tmp, "/x /bbb 301\n")
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}
	if got := target(h.pollOnce(file, f)); got != "/bbb" {
		t.Fatalf("atomic replace with preserved mtime: expected /bbb, got %s", got)
	}

	// In-place rewrite with the same mtime but a different size (cp -p).
	write(file, "/x /cccc 301\n")
	if got := target(h.pollOnce(file, f)); got != "/cccc" {
		t.Fatalf("in-place rewrite with preserved mtime: expected /cccc, got %s", got)
	}
}

func TestWatcherFollowsReplacedRootDirectory(t *testing.T) {
	tests := []struct {
		name string
		swap func(t *testing.T, base, root string) string // returns the new real directory
	}{
		{
			name: "symlink swap",
			swap: func(t *testing.T, base, root string) string {
				next := filepath.Join(base, "release-2")
				if err := os.Mkdir(next, 0755); err != nil {
					t.Fatal(err)
				}
				tmpLink := filepath.Join(base, "current.tmp")
				if err := os.Symlink(next, tmpLink); err != nil {
					t.Skipf("symlinks not supported: %v", err)
				}
				if err := os.Rename(tmpLink, root); err != nil {
					t.Fatal(err)
				}
				return next
			},
		},
		{
			name: "directory swap",
			swap: func(t *testing.T, base, root string) string {
				if err := os.Rename(root, filepath.Join(base, "old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0755); err != nil {
					t.Fatal(err)
				}
				return root
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "current")
			if tt.name == "symlink swap" {
				first := filepath.Join(base, "release-1")
				if err := os.Mkdir(first, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(first, root); err != nil {
					t.Skipf("symlinks not supported: %v", err)
				}
			} else if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "_redirects")
			if err := os.WriteFile(file, []byte("/x /v1 301\n"), 0644); err != nil {
				t.Fatal(err)
			}

			h := newTestRedirectHandler()
			defer h.Cleanup()
			h.getCached(file)
			hasWatcher := h.hasWatcher()
			if !hasWatcher {
				t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
			}

			next := tt.swap(t, base, root)
			if err := os.WriteFile(filepath.Join(next, "_redirects"), []byte("/x /v2 301\n"), 0644); err != nil {
				t.Fatal(err)
			}
			// The next check notices the swap, reloads and moves the watch.
			h.expireCache()
			if match := h.getCompiledRedirects(file).MatchFirst("/x"); match == nil || match.To != "/v2" {
				t.Fatalf("expected /v2 after swap, got %+v", match)
			}

			// An edit in the new directory must now arrive via the watcher alone.
			if err := os.WriteFile(filepath.Join(next, "_redirects"), []byte("/x /v3 301\n"), 0644); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if c, _ := h.cachedData(file).(*CompiledRedirects); c != nil {
					if match := c.MatchFirst("/x"); match != nil && match.To == "/v3" {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("timed out: watcher did not pick up an edit in the new root directory")
		})
	}
}

// hubHandlers returns how many handlers the shared watcher notifies for dir.
func hubHandlers(dir string) int {
	sharedWatcher.mu.Lock()
	defer sharedWatcher.mu.Unlock()
	if d := sharedWatcher.dirs[dir]; d != nil {
		return len(d.handlers)
	}
	return 0
}

func TestSharedWatcherAcrossHandlers(t *testing.T) {
	dir := t.TempDir()
	headersFile := filepath.Join(dir, "_headers")
	redirectsFile := filepath.Join(dir, "_redirects")
	if err := os.WriteFile(headersFile, []byte("/x\n  X-V: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(redirectsFile, []byte("/x /v1 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	hh := newTestHeadersHandler()
	rh := newTestRedirectHandler()
	defer hh.Cleanup()
	defer rh.Cleanup()
	hh.getCompiled(headersFile)
	rh.getCompiledRedirects(redirectsFile)
	if !hh.hasWatcher() || !rh.hasWatcher() {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	if n := hubHandlers(dir); n != 2 {
		t.Fatalf("expected both handlers on one shared watch, got %d", n)
	}

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	headerValue := func() string {
		c, _ := hh.cachedData(headersFile).(*Compiled)
		if c == nil {
			return ""
		}
		ops := c.MatchOrdered("/x", "")
		if len(ops) != 1 {
			return ""
		}
		return ops[0].Value
	}
	redirectTarget := func() string {
		c, _ := rh.cachedData(redirectsFile).(*CompiledRedirects)
		if c == nil || c.MatchFirst("/x") == nil {
			return ""
		}
		return c.MatchFirst("/x").To
	}

	if err := os.WriteFile(headersFile, []byte("/x\n  X-V: 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(redirectsFile, []byte("/x /v2 301\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitFor("headers reload", func() bool { return headerValue() == "2" })
	waitFor("redirects reload", func() bool { return redirectTarget() == "/v2" })

	// The remaining handler keeps getting events after the other is cleaned up.
	hh.Cleanup()
	if n := hubHandlers(dir); n != 1 {
		t.Fatalf("expected 1 handler after cleanup, got %d", n)
	}
	if err := os.WriteFile(redirectsFile, []byte("/x /v3 301\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitFor("redirects reload after other handler's cleanup", func() bool { return redirectTarget() == "/v3" })

	rh.Cleanup()
	if n := hubHandlers(dir); n != 0 {
		t.Fatalf("expected the watch to be removed after all cleanups, got %d handlers", n)
	}
}

// TestWatcherSharedDirectoryViaSymlink covers two roots that are the same
// directory. inotify gives both paths one watch, which fsnotify knows by the
// first path only, so events must reach both and cleaning up either must
// leave the other watched.
func TestWatcherSharedDirectoryViaSymlink(t *testing.T) {
	for _, cleanupFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("cleanup first subscriber %v", cleanupFirst), func(t *testing.T) {
			base := t.TempDir()
			real := filepath.Join(base, "real")
			link := filepath.Join(base, "link")
			if err := os.Mkdir(real, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Skip("symlinks not supported:", err)
			}
			if err := os.WriteFile(filepath.Join(real, "_headers"), []byte("/x\n  X-V: 1\n"), 0644); err != nil {
				t.Fatal(err)
			}

			realFile, linkFile := filepath.Join(real, "_headers"), filepath.Join(link, "_headers")
			first, second := newTestHeadersHandler(), newTestHeadersHandler()
			defer first.Cleanup()
			defer second.Cleanup()
			first.getCompiled(realFile)
			second.getCompiled(linkFile)
			if !first.hasWatcher() || !second.hasWatcher() {
				t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
			}

			value := func(h *Handler, file string) string {
				c, _ := h.cachedData(file).(*Compiled)
				if c == nil {
					return ""
				}
				ops := c.MatchOrdered("/x", "")
				if len(ops) != 1 {
					return ""
				}
				return ops[0].Value
			}
			// cachedData never polls, and the deadline is below checkInterval,
			// so only watcher events can satisfy these.
			waitFor := func(what string, cond func() bool) {
				t.Helper()
				deadline := time.Now().Add(checkInterval / 2)
				for time.Now().Before(deadline) {
					if cond() {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("timed out waiting for %s", what)
			}

			if err := os.WriteFile(realFile, []byte("/x\n  X-V: 2\n"), 0644); err != nil {
				t.Fatal(err)
			}
			waitFor("reload via real path", func() bool { return value(first, realFile) == "2" })
			waitFor("reload via symlink", func() bool { return value(second, linkFile) == "2" })

			gone, remaining, remainingFile := first, second, linkFile
			if !cleanupFirst {
				gone, remaining, remainingFile = second, first, realFile
			}
			gone.Cleanup()
			if err := os.WriteFile(realFile, []byte("/x\n  X-V: 3\n"), 0644); err != nil {
				t.Fatal(err)
			}
			waitFor("reload after the other handler's cleanup", func() bool { return value(remaining, remainingFile) == "3" })

			remaining.Cleanup()
			if n := hubHandlers(real) + hubHandlers(link); n != 0 {
				t.Fatalf("expected no subscriptions after all cleanups, got %d handlers", n)
			}
		})
	}
}

func TestDueCheckDoesNotBlockOtherRequests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "_redirects")
	writeWithMtime(t, file, "/x /v1 301\n", 0)

	h := newTestRedirectHandler()
	defer h.Cleanup()
	h.getCompiledRedirects(file)

	release := make(chan struct{})
	loading := make(chan struct{})
	load := h.loadFn
	h.loadFn = func(p string) (any, error) {
		close(loading)
		<-release
		return load(p)
	}

	writeWithMtime(t, file, "/x /v2 301\n", 10*time.Second)
	h.expireCache()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.getCompiledRedirects(file) // wins the check and blocks in loadFn
	}()
	<-loading

	served := make(chan string, 1)
	go func() { served <- h.getCompiledRedirects(file).MatchFirst("/x").To }()
	select {
	case to := <-served:
		if to != "/v1" {
			t.Errorf("expected the current rules (/v1) during the reload, got %s", to)
		}
	case <-time.After(time.Second):
		t.Fatal("request blocked behind a reload in progress")
	}

	close(release)
	<-done
	if to := h.getCompiledRedirects(file).MatchFirst("/x").To; to != "/v2" {
		t.Errorf("expected /v2 after the reload, got %s", to)
	}
}

func TestGetCachedConcurrentWithReloads(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "_redirects")
	writeWithMtime(t, file, "/x /v0 301\n", 0)

	h := newTestRedirectHandler()
	defer h.Cleanup()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if c := h.getCompiledRedirects(file); c == nil || c.MatchFirst("/x") == nil {
					t.Error("expected a rule for /x on every request")
					return
				}
			}
		}()
	}

	for i := 1; i <= 20; i++ {
		writeWithMtime(t, file, fmt.Sprintf("/x /v%d 301\n", i), time.Duration(i)*time.Second)
		h.expireCache()
		h.reloadInBackground(file)
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

func TestCheckOfEvictedEntryDoesNotResubscribe(t *testing.T) {
	base := t.TempDir()
	file := func(i int) string {
		dir := filepath.Join(base, fmt.Sprint(i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, "_redirects")
	}

	h := newTestRedirectHandler()
	victim := file(0)
	h.getCached(victim)
	for i := 1; i < maxCachedFiles; i++ {
		h.getCached(file(i))
	}
	if !h.hasWatcher() {
		h.Cleanup()
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}
	f := h.lookup(victim)
	f.expire()
	stale := f.lastCheck.Load()

	// Hold mu so the request below wins the check but waits before running it.
	h.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.getCached(victim)
	}()
	for f.lastCheck.Load() == stale {
		time.Sleep(time.Millisecond)
	}

	// Evict the entry while the check waits, as if many other roots were
	// added in the meantime.
	f.expire()
	h.addFileLocked(file(maxCachedFiles), &cachedFile{})
	h.mu.Unlock()
	<-done

	if h.lookup(victim) != nil {
		t.Fatal("expected the victim to be evicted")
	}
	h.Cleanup()
	if n := hubHandlers(filepath.Dir(victim)); n != 0 {
		t.Errorf("expected no watch on the evicted root after Cleanup, got %d handlers", n)
	}
}

func TestEvictionPrefersMissingFiles(t *testing.T) {
	loaded := filepath.Join(t.TempDir(), "_redirects")
	if err := os.WriteFile(loaded, []byte("/x /a 301\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestRedirectHandler()
	defer h.Cleanup()
	h.getCached(loaded)
	h.lookup(loaded).expire() // the least recently checked entry

	base := t.TempDir()
	for i := 0; i < 2*maxCachedFiles; i++ {
		h.getCached(filepath.Join(base, fmt.Sprint(i), "_redirects"))
	}

	if h.cachedData(loaded) == nil {
		t.Error("entries for missing files evicted the loaded rules")
	}
}

// hubWatchInfo returns the identity of the directory the shared watcher
// watches for dir, or nil when dir has no watch.
func hubWatchInfo(dir string) os.FileInfo {
	sharedWatcher.mu.Lock()
	defer sharedWatcher.mu.Unlock()
	if d := sharedWatcher.dirs[dir]; d != nil && d.watch != nil {
		return d.watch.info
	}
	return nil
}

// headerValueOf returns the X-V header that the rules cached by h for file
// set on /x, without triggering a check.
func headerValueOf(h *Handler, file string) string {
	c, _ := h.cachedData(file).(*Compiled)
	if c == nil {
		return ""
	}
	ops := c.MatchOrdered("/x", "")
	if len(ops) != 1 {
		return ""
	}
	return ops[0].Value
}

// TestWatcherRewatchesDirectoryRenamedBack covers a root renamed away and
// back. It keeps its identity, but fsnotify stops watching it on the rename,
// so the hub must forget the watch for the next check to add it again.
func TestWatcherRewatchesDirectoryRenamedBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "site")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "_headers")
	if err := os.WriteFile(file, []byte("/x\n  X-V: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHeadersHandler()
	defer h.Cleanup()
	h.getCompiled(file)
	if !h.hasWatcher() {
		t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
	}

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(checkInterval / 2)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	if err := os.Rename(root, root+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root+".tmp", root); err != nil {
		t.Fatal(err)
	}
	waitFor("the hub to drop the watch", func() bool { return hubWatchInfo(root) == nil })

	h.expireCache()
	h.getCompiled(file)
	if hubWatchInfo(root) == nil {
		t.Fatal("expected the check to watch the directory again")
	}

	// cachedData never polls, and the deadline is below checkInterval, so
	// only a watcher event can satisfy this.
	if err := os.WriteFile(file, []byte("/x\n  X-V: 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitFor("reload via the new watch", func() bool { return headerValueOf(h, file) == "2" })
}

// TestWatcherReattachesSharedPathsByCurrentTarget covers three symlinks to
// one directory sharing a watch known by the first. When the first and
// second are re-pointed, the remaining paths must be attached to watches on
// the directories they lead to now, not to the old one.
func TestWatcherReattachesSharedPathsByCurrentTarget(t *testing.T) {
	base := t.TempDir()
	mkdir := func(name string) string {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	a, b, c := mkdir("a"), mkdir("b"), mkdir("c")
	if err := os.WriteFile(filepath.Join(a, "_headers"), []byte("/x\n  X-V: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	links := []string{filepath.Join(base, "link0"), filepath.Join(base, "link1"), filepath.Join(base, "link2")}
	for _, link := range links {
		if err := os.Symlink(a, link); err != nil {
			t.Skip("symlinks not supported:", err)
		}
	}
	repoint := func(link, target string) {
		tmp := link + ".tmp"
		if err := os.Symlink(target, tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, link); err != nil {
			t.Fatal(err)
		}
	}

	handlers := make([]*Handler, len(links))
	for i, link := range links {
		handlers[i] = newTestHeadersHandler()
		defer handlers[i].Cleanup()
		handlers[i].getCompiled(filepath.Join(link, "_headers"))
		if !handlers[i].hasWatcher() {
			t.Skip("fsnotify watcher not available on this platform; skipping watcher test")
		}
	}

	repoint(links[0], b)
	repoint(links[1], c)
	// link0's check moves its watch, re-attaching link1 and link2.
	handlers[0].expireCache()
	handlers[0].getCompiled(filepath.Join(links[0], "_headers"))

	for _, link := range links {
		info, err := os.Stat(link)
		if err != nil {
			t.Fatal(err)
		}
		if watched := hubWatchInfo(link); watched == nil || !os.SameFile(watched, info) {
			t.Errorf("%s: watch is not on the directory it leads to", filepath.Base(link))
		}
	}

	// link2 still leads to a, so an edit there must reach it via the watcher.
	file := filepath.Join(links[2], "_headers")
	if err := os.WriteFile(filepath.Join(a, "_headers"), []byte("/x\n  X-V: 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(checkInterval / 2)
	for headerValueOf(handlers[2], file) != "2" {
		if time.Now().After(deadline) {
			t.Fatal("timed out: link2 did not get the edit via the watcher")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
