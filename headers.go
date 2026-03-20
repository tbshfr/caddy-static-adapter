package staticadapter

import (
	"net"
	"net/http"
	"os"
	"path/filepath"

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
		ops := compiled.MatchOrdered(r.URL.Path, host)
		applyHeaders(w.Header(), ops)
	}

	return next.ServeHTTP(w, r)
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
func (h *Handler) loadFile(filePath string) any {
	f, err := os.Open(filePath)
	if err != nil {
		h.logger.Warn(h.logPrefix+": failed to open file",
			zap.String("file", filePath),
			zap.Error(err))
		return nil
	}
	defer f.Close()

	rules, warnings := Parse(f, h.MaxRules, h.MaxFileSize, h.MaxLineLength)
	for _, w := range warnings {
		if h.Strict {
			h.logger.Error(h.logPrefix+": parse error (strict mode)", zap.Error(w))
			return nil
		}
		h.logger.Warn(h.logPrefix+": parse warning", zap.Error(w))
	}

	compiled := Compile(rules)
	h.logger.Info(h.logPrefix+": loaded rules",
		zap.String("file", filePath),
		zap.Int("total_rules", len(rules)))
	return compiled
}

// applyHeaders applies the given header operations to the response headers.
// Per Cloudflare semantics:
//   - Normal headers are set; if the same header appears multiple times,
//     values are joined with a comma separator.
//   - Headers prefixed with ! are removed.
func applyHeaders(headers http.Header, ops []HeaderOp) {
	for _, op := range ops {
		switch op.Mode {
		case OpSet:
			existing := headers.Get(op.Name)
			if existing == "" {
				headers.Set(op.Name, op.Value)
			} else {
				headers.Set(op.Name, existing+", "+op.Value)
			}
		case OpRemove:
			headers.Del(op.Name)
		}
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
// Syntax:
//
//	static_headers {
//	    strict
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
