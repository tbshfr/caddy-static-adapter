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
	dirs    map[string]*hubDir
}

type hubDir struct {
	info     os.FileInfo // identity of the directory the watch was added for
	handlers map[*fileHandler]struct{}
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
			h.removeLocked(dir)
		}
		return statErr
	}
	if d != nil && os.SameFile(d.info, info) {
		d.handlers[fh] = struct{}{}
		return nil
	}

	if h.watcher == nil {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("failed to create file watcher: %w", err)
		}
		h.watcher = watcher
		h.dirs = make(map[string]*hubDir)
		go h.loop(watcher)
	}

	if d != nil {
		_ = h.watcher.Remove(dir)
	}
	if err := h.watcher.Add(dir); err != nil {
		if d != nil {
			delete(h.dirs, dir)
		}
		h.closeIfIdleLocked()
		return err
	}

	if d == nil {
		d = &hubDir{handlers: make(map[*fileHandler]struct{})}
		h.dirs[dir] = d
	}
	d.info = info
	d.handlers[fh] = struct{}{}
	return nil
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
		h.removeLocked(dir)
	}
}

// removeLocked drops the watch on dir for all handlers. Must be called with
// h.mu held.
func (h *watchHub) removeLocked(dir string) {
	_ = h.watcher.Remove(dir)
	delete(h.dirs, dir)
	h.closeIfIdleLocked()
}

// closeIfIdleLocked closes the watcher once no directory is watched. Its
// event loop exits when fsnotify closes the event channels. Must be called
// with h.mu held.
func (h *watchHub) closeIfIdleLocked() {
	if h.watcher != nil && len(h.dirs) == 0 {
		h.watcher.Close()
		h.watcher = nil
	}
}

// handlersFor returns the handlers subscribed to dir.
func (h *watchHub) handlersFor(dir string) []*fileHandler {
	h.mu.Lock()
	defer h.mu.Unlock()

	d := h.dirs[dir]
	if d == nil {
		return nil
	}
	handlers := make([]*fileHandler, 0, len(d.handlers))
	for fh := range d.handlers {
		handlers = append(handlers, fh)
	}
	return handlers
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
			filePath := filepath.Clean(event.Name)
			for _, fh := range h.handlersFor(filepath.Dir(filePath)) {
				if filepath.Base(filePath) == fh.fileName {
					fh.scheduleReload(filePath)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			caddy.Log().Named("static_adapter").Warn("file watcher error", zap.Error(err))
		}
	}
}
