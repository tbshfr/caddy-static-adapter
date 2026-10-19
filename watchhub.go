package staticadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// sharedWatcher is the process-wide filesystem watcher. All handlers share
// it, so any number of sites and directives use a single inotify instance;
// Linux limits those per user (fs.inotify.max_user_instances, often 128).
var sharedWatcher = &watchHub{}

// watchHub multiplexes one fsnotify watcher across handlers. Directories
// are reference counted, and the watcher is closed once nothing is
// subscribed.
type watchHub struct {
	mu      sync.Mutex
	watcher *fsnotify.Watcher
	dirs    map[string]*hubDir   // by subscribed directory path
	watches map[string]*hubWatch // by the path given to watcher.Add
}

// hubWatch is one fsnotify watch. inotify watches inodes, so subscribed
// paths that resolve to the same directory (a symlink and its target, a bind
// mount) must share one: adding the second path returns the same watch
// descriptor, fsnotify names its events after the first path only, and
// removing either path removes the watch for both.
type hubWatch struct {
	path string      // path given to watcher.Add; fsnotify names events after it
	info os.FileInfo // identity of the watched directory
	dirs map[string]*hubDir
}

type hubDir struct {
	watch    *hubWatch // nil until attached, or after the path it shared was removed
	handlers map[*fileHandler]struct{}
}

// reloadTarget is a handler to notify about a changed file, with the path
// under which that handler caches the file.
type reloadTarget struct {
	fh       *fileHandler
	filePath string
}

// subscribe makes sure dir is watched and fh is notified about changes in
// it. It is safe to call repeatedly; each call also checks that the watch
// still belongs to the directory now at that path. A watch follows the
// directory it was added for, so after a deploy replaced the directory (a
// renamed release directory or a re-pointed symlink) it is moved to the
// new one.
func (h *watchHub) subscribe(dir string, fh *fileHandler) error {
	info, statErr := os.Stat(dir)

	h.mu.Lock()
	defer h.mu.Unlock()

	d := h.dirs[dir]
	if statErr != nil {
		if d != nil {
			h.removeLocked(dir, d)
		}
		return statErr
	}
	if d != nil && d.watch != nil && os.SameFile(d.watch.info, info) {
		d.handlers[fh] = struct{}{}
		return nil
	}

	if d == nil {
		d = &hubDir{handlers: make(map[*fileHandler]struct{})}
	} else {
		h.detachLocked(dir, d)
	}
	if err := h.attachLocked(dir, d, info); err != nil {
		// Other handlers of dir subscribe again on their next check.
		delete(h.dirs, dir)
		h.closeIfIdleLocked()
		return err
	}
	if h.dirs == nil {
		h.dirs = make(map[string]*hubDir)
	}
	h.dirs[dir] = d
	d.handlers[fh] = struct{}{}
	return nil
}

// attachLocked attaches d to the watch of the directory described by info,
// adding a watch for dir when no subscribed path resolves to that directory
// yet. Must be called with h.mu held.
func (h *watchHub) attachLocked(dir string, d *hubDir, info os.FileInfo) error {
	for _, w := range h.watches {
		if os.SameFile(w.info, info) {
			w.dirs[dir] = d
			d.watch = w
			return nil
		}
	}

	if h.watcher == nil {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("failed to create file watcher: %w", err)
		}
		h.watcher = watcher
		h.watches = make(map[string]*hubWatch)
		go h.loop(watcher)
	}
	if err := h.watcher.Add(dir); err != nil {
		return err
	}
	w := &hubWatch{path: dir, info: info, dirs: map[string]*hubDir{dir: d}}
	h.watches[dir] = w
	d.watch = w
	return nil
}

// detachLocked detaches d from its watch. When dir is the path fsnotify
// knows the watch by, the watch is removed and the other paths to the same
// directory move to a new watch under one of them. Must be called with h.mu
// held.
func (h *watchHub) detachLocked(dir string, d *hubDir) {
	w := d.watch
	if w == nil {
		return
	}
	d.watch = nil
	delete(w.dirs, dir)
	if w.path != dir {
		return
	}
	_ = h.watcher.Remove(w.path)
	delete(h.watches, w.path)
	for p, other := range w.dirs {
		other.watch = nil
		// On failure, the next subscribe of p retries, as other.watch is nil.
		_ = h.attachLocked(p, other, w.info)
	}
}

// unsubscribe stops notifying fh about dir, removing the watch when no
// handler needs it anymore.
func (h *watchHub) unsubscribe(dir string, fh *fileHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()

	d := h.dirs[dir]
	if d == nil {
		return
	}
	delete(d.handlers, fh)
	if len(d.handlers) == 0 {
		h.removeLocked(dir, d)
	}
}

// removeLocked drops the subscription of dir for all handlers. Must be
// called with h.mu held.
func (h *watchHub) removeLocked(dir string, d *hubDir) {
	h.detachLocked(dir, d)
	delete(h.dirs, dir)
	h.closeIfIdleLocked()
}

// closeIfIdleLocked closes the watcher once no directory is watched. Its
// event loop exits when fsnotify closes the event channels. Must be called
// with h.mu held.
func (h *watchHub) closeIfIdleLocked() {
	if h.watcher != nil && len(h.watches) == 0 {
		h.watcher.Close()
		h.watcher = nil
	}
}

// targetsFor returns the handlers to notify about a change to filePath, a
// file in a watched directory as fsnotify names it, under every subscribed
// path to that directory.
func (h *watchHub) targetsFor(filePath string) []reloadTarget {
	h.mu.Lock()
	defer h.mu.Unlock()

	w := h.watches[filepath.Dir(filePath)]
	if w == nil {
		return nil
	}
	name := filepath.Base(filePath)
	var targets []reloadTarget
	for dir, d := range w.dirs {
		for fh := range d.handlers {
			if fh.fileName == name {
				targets = append(targets, reloadTarget{fh: fh, filePath: filepath.Join(dir, name)})
			}
		}
	}
	return targets
}

// loop forwards events for watched files to the handlers subscribed to
// their directory.
func (h *watchHub) loop(watcher *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			for _, t := range h.targetsFor(filepath.Clean(event.Name)) {
				t.fh.scheduleReload(t.filePath)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			caddy.Log().Named("static_adapter").Warn("file watcher error", zap.Error(err))
		}
	}
}
