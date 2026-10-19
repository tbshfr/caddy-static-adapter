package staticadapter

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(new(Handler))
	httpcaddyfile.RegisterHandlerDirective("static_headers", parseCaddyfile)
}

// Handler implements an HTTP handler that applies headers from a
// Cloudflare Pages / Netlify-compatible _headers file. The file is
// automatically located at {root}/_headers (using Caddy's root variable)
// and reloaded when it changes on disk. A filesystem watcher provides
// instant reload on file changes; time-based polling acts as a fallback.
type Handler struct {
	fileHandler
}

// CaddyModule returns the Caddy module information.
func (*Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.static_headers",
		New: func() caddy.Module { return new(Handler) },
	}
}

// Provision sets up the handler.
func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()
	h.logPrefix = "static_headers"
	h.fileName = "_headers"
	h.loadFn = h.loadFile
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Block direct access to the _headers configuration file.
	// Fall back to full normalization only for suspicious paths such as
	// duplicate slashes, dot segments, or trailing slashes.
	if isProtectedConfigPath(r.URL.Path, "/_headers") {
		return caddyhttp.Error(http.StatusNotFound, nil)
	}

	repl, ok := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	if !ok || repl == nil {
		return next.ServeHTTP(w, r)
	}

	root := repl.ReplaceAll("{http.vars.root}", ".")
	filePath := filepath.Join(root, "_headers")

	compiled := h.getCompiled(filePath)
	if compiled != nil {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		path := cleanRequestPath(r.URL.Path)
		var ops []HeaderOp
		if h.Dedup {
			ops = compiled.MatchBySpecificity(path, host)
		} else {
			ops = compiled.MatchOrdered(path, host)
		}
		applyHeaders(w.Header(), ops, h.Dedup)
	}

	return next.ServeHTTP(w, r)
}

// windowsPaths is whether file_server resolves request paths to files the
// way Windows does (see windowsPath). Tests change it.
var windowsPaths = runtime.GOOS == "windows"

// cleanRequestPath resolves dot segments and merges repeated slashes, as
// file_server does when it maps a request path to a file, and on Windows
// also resolves the path the way Windows would. Matching the raw path would
// let //admin/x, /a/../admin/x or, on Windows, /admin\x be served the file
// /admin/x without the headers of an /admin/* rule. Clean paths, the common
// case, are returned as they are, without allocating.
func cleanRequestPath(p string) string {
	if windowsPaths && hasWindowsPathQuirks(p) {
		return caddyhttp.CleanPath(windowsPath(p), true)
	}
	for i := 0; i < len(p); i++ {
		if p[i] != '/' {
			continue
		}
		switch rest := p[i+1:]; {
		case strings.HasPrefix(rest, "/"), strings.HasPrefix(rest, "./"), strings.HasPrefix(rest, "../"),
			rest == ".", rest == "..":
			return caddyhttp.CleanPath(p, true)
		}
	}
	return p
}

// hasWindowsPathQuirks reports whether p contains a backslash or a segment
// ending in a dot or space, which windowsPath would change.
func hasWindowsPathQuirks(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			return true
		case '.', ' ':
			if i+1 == len(p) || p[i+1] == '/' {
				return true
			}
		}
	}
	return false
}

// getCompiled returns the compiled header rules for the given file path.
func (h *Handler) getCompiled(filePath string) *Compiled {
	result := h.getCached(filePath)
	if result == nil {
		return nil
	}
	return result.(*Compiled)
}

// loadFile reads and compiles the _headers file. Called by the shared
// fileHandler infrastructure under write lock.
func (h *Handler) loadFile(filePath string) (any, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	rules, warnings := Parse(f, h.MaxRules, h.MaxFileSize, h.MaxLineLength)
	if h.Strict {
		if err := firstStrictError(warnings); err != nil {
			return nil, fmt.Errorf("parse error (strict mode): %w", err)
		}
	}
	for _, w := range warnings {
		h.logger.Warn(h.logPrefix+": parse warning", zap.Error(w))
	}

	foldCase := false
	if info, err := f.Stat(); err == nil {
		foldCase = caseInsensitiveDir(filePath, info)
	}
	compiled := compileRules(rules, foldCase)
	h.logger.Info(h.logPrefix+": loaded rules",
		zap.String("file", filePath),
		zap.Int("total_rules", len(rules)),
		zap.Bool("case_insensitive", foldCase))
	return compiled, nil
}

// caseInsensitiveDir reports whether the file at filePath, described by
// info, is also found under its name in upper case, as on the default
// filesystems of macOS and Windows. file_server then serves /ADMIN/x as the
// file /admin/x, so rules must match regardless of case for it to get the
// headers of an /admin/* rule.
func caseInsensitiveDir(filePath string, info os.FileInfo) bool {
	dir, name := filepath.Split(filePath)
	upper := strings.ToUpper(name)
	if upper == name {
		return false
	}
	other, err := statIdentity(filepath.Join(dir, upper))
	return err == nil && os.SameFile(info, other)
}

// applyHeaders applies the given header operations to the response headers.
// Per Cloudflare semantics:
//   - Normal headers are set; if the same header appears multiple times,
//     values are joined with a comma separator.
//   - Headers prefixed with ! are removed.
//
// When dedup is true, duplicate header names across matching rules use
// "last wins" semantics (overwrite) instead of comma-joining. This is
// useful for _headers files with overlapping wildcard rules that set the
// same header at different specificity levels.
//
// Set-Cookie is never comma-joined: each value becomes its own header line.
func applyHeaders(headers http.Header, ops []HeaderOp, dedup bool) {
	for _, op := range ops {
		switch op.Mode {
		case OpSet:
			if dedup {
				headers.Set(op.Name, op.Value)
			} else if noCommaJoin(op.Name) {
				headers.Add(op.Name, op.Value)
			} else {
				existing := headers.Get(op.Name)
				if existing == "" {
					headers.Set(op.Name, op.Value)
				} else {
					headers.Set(op.Name, existing+", "+op.Value)
				}
			}
		case OpRemove:
			headers.Del(op.Name)
		case opAppend:
			headers.Add(op.Name, op.Value)
		}
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
// Syntax:
//
//	static_headers {
//	    strict
//	    dedup
//	    max_rules <number>
//	    max_file_size <size>
//	    max_line_length <number>
//	}
func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	return h.unmarshalCaddyfile(d)
}

// parseCaddyfile unmarshals tokens from h into a new Handler.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Handler
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return &m, err
}

// Interface guards
var (
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
	_ caddyfile.Unmarshaler       = (*Handler)(nil)
)
