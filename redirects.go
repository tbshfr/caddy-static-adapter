package staticadapter

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(new(RedirectHandler))
	httpcaddyfile.RegisterHandlerDirective("static_redirects", parseRedirectCaddyfile)
}

// RedirectHandler implements an HTTP handler that applies redirects from a
// Cloudflare Pages / Netlify-compatible _redirects file. The file is
// automatically located at {root}/_redirects (using Caddy's root variable)
// and reloaded when it changes on disk. A filesystem watcher provides
// instant reload on file changes; time-based polling acts as a fallback.
type RedirectHandler struct {
	fileHandler
}

// compiledRedirect holds a parsed redirect rule with its precomputed path pattern.
type compiledRedirect struct {
	*RedirectRule
	pathPattern string
}

// indexedRedirect pairs a compiledRedirect with its original file position.
type indexedRedirect struct {
	index int
	cr    *compiledRedirect
}

// CompiledRedirects holds redirect rules indexed for efficient first-match lookup.
// Exact-path rules are stored in a map for O(1) lookup; wildcard/placeholder
// rules are kept in a separate slice. Both preserve original definition order
// for correct first-match-wins semantics.
type CompiledRedirects struct {
	// exactRedirects maps normalized path → indexed redirects with that exact path.
	exactRedirects map[string][]indexedRedirect
	// wildcards holds redirect rules containing * or : patterns in definition order.
	wildcards []indexedRedirect
}

// compileRedirects creates an indexed CompiledRedirects from a list of
// compiled redirect rules.
func compileRedirects(rules []*compiledRedirect) *CompiledRedirects {
	cr := &CompiledRedirects{
		exactRedirects: make(map[string][]indexedRedirect, len(rules)),
	}
	for i, r := range rules {
		ir := indexedRedirect{index: i, cr: r}
		if hasWildcardOrPlaceholder(r.pathPattern) {
			cr.wildcards = append(cr.wildcards, ir)
		} else {
			cr.exactRedirects[r.pathPattern] = append(cr.exactRedirects[r.pathPattern], ir)
		}
	}
	return cr
}

// MatchFirst returns the first matching redirect rule for the given request
// path, or nil if no rule matches. This preserves first-match-wins semantics
// by comparing original indices across exact and wildcard buckets.
func (cr *CompiledRedirects) MatchFirst(requestPath string) *compiledRedirect {
	requestPath = normalizePath(requestPath)

	// Find best exact match (first by definition order).
	exactCandidates := cr.exactRedirects[requestPath]
	var bestExact *indexedRedirect
	if len(exactCandidates) > 0 {
		bestExact = &exactCandidates[0] // already in definition order
	}

	// Scan wildcards, looking for a match with a lower index than the best exact.
	for _, ir := range cr.wildcards {
		// If we already have an exact match and this wildcard comes after it,
		// stop — no wildcard can beat the exact match.
		if bestExact != nil && ir.index > bestExact.index {
			break
		}
		if matchRedirect(ir.cr, requestPath) {
			return ir.cr
		}
	}

	if bestExact != nil {
		return bestExact.cr
	}

	return nil
}

// totalRules returns the total number of compiled redirect rules across all buckets.
func (cr *CompiledRedirects) totalRules() int {
	total := len(cr.wildcards)
	for _, rules := range cr.exactRedirects {
		total += len(rules)
	}
	return total
}

// CaddyModule returns the Caddy module information.
func (*RedirectHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.static_redirects",
		New: func() caddy.Module { return new(RedirectHandler) },
	}
}

// Provision sets up the handler.
func (h *RedirectHandler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()
	h.logPrefix = "static_redirects"
	h.fileName = "_redirects"
	h.loadFn = h.loadRedirectFile
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h *RedirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Block direct access to the _redirects configuration file.
	// Fall back to full normalization only for suspicious paths such as
	// duplicate slashes, dot segments, or trailing slashes.
	if isProtectedConfigPath(r.URL.Path, "/_redirects") {
		return caddyhttp.Error(http.StatusNotFound, nil)
	}

	repl, ok := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	if !ok || repl == nil {
		return next.ServeHTTP(w, r)
	}

	root := repl.ReplaceAll("{http.vars.root}", ".")
	filePath := filepath.Join(root, "_redirects")

	compiled := h.getCompiledRedirects(filePath)
	if compiled != nil {
		if match := compiled.MatchFirst(r.URL.Path); match != nil {
			to := expandRedirectTo(match.pathPattern, match.To, normalizePath(r.URL.Path))
			http.Redirect(w, r, to, match.Status)
			return nil
		}
	}

	return next.ServeHTTP(w, r)
}

// getCompiledRedirects returns the compiled redirect rules for the given file path.
func (h *RedirectHandler) getCompiledRedirects(filePath string) *CompiledRedirects {
	result := h.getCached(filePath)
	if result == nil {
		return nil
	}
	return result.(*CompiledRedirects)
}

// loadRedirectFile reads and compiles the _redirects file. Called by the
// shared fileHandler infrastructure under write lock.
func (h *RedirectHandler) loadRedirectFile(filePath string) any {
	f, err := os.Open(filePath)
	if err != nil {
		h.logger.Warn(h.logPrefix+": failed to open file",
			zap.String("file", filePath),
			zap.Error(err))
		return nil
	}
	defer f.Close()

	parsed, warnings := ParseRedirects(f, h.MaxRules, h.MaxFileSize, h.MaxLineLength)
	for _, w := range warnings {
		if h.Strict {
			h.logger.Error(h.logPrefix+": parse error (strict mode)", zap.Error(w))
			return nil
		}
		h.logger.Warn(h.logPrefix+": parse warning", zap.Error(w))
	}

	rules := make([]*compiledRedirect, len(parsed))
	for i, r := range parsed {
		rules[i] = &compiledRedirect{
			RedirectRule: r,
			pathPattern:  r.From,
		}
	}

	compiled := compileRedirects(rules)

	h.logger.Info(h.logPrefix+": loaded redirect rules",
		zap.String("file", filePath),
		zap.Int("total_rules", len(rules)))
	return compiled
}

// matchRedirect checks whether a request path matches a compiled redirect rule.
func matchRedirect(cr *compiledRedirect, requestPath string) bool {
	if hasWildcardOrPlaceholder(cr.pathPattern) {
		return matchPattern(cr.pathPattern, requestPath)
	}
	return cr.pathPattern == requestPath
}

// expandRedirectTo expands placeholders and :splat in the redirect destination.
func expandRedirectTo(pattern, to, requestPath string) string {
	if !strings.Contains(to, ":") {
		return to
	}

	// Extract splat value if pattern has wildcard.
	if idx := strings.Index(pattern, "*"); idx >= 0 {
		prefix := pattern[:idx]
		suffix := pattern[idx+1:]
		remaining := requestPath[len(prefix):]
		if suffix != "" {
			remaining = strings.TrimSuffix(remaining, suffix)
		}
		to = strings.ReplaceAll(to, ":splat", remaining)
	}

	// Extract placeholder values.
	if placeholders, ok := extractPlaceholders(pattern, requestPath); ok {
		for name, val := range placeholders {
			to = strings.ReplaceAll(to, ":"+name, val)
		}
	}

	return to
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
// Syntax:
//
//	static_redirects {
//	    strict
//	    max_rules <number>
//	    max_file_size <size>
//	    max_line_length <number>
//	}
func (h *RedirectHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	return h.unmarshalCaddyfile(d)
}

// parseRedirectCaddyfile unmarshals tokens from h into a new RedirectHandler.
func parseRedirectCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m RedirectHandler
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return &m, err
}

// Interface guards
var (
	_ caddy.Provisioner           = (*RedirectHandler)(nil)
	_ caddy.CleanerUpper          = (*RedirectHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*RedirectHandler)(nil)
	_ caddyfile.Unmarshaler       = (*RedirectHandler)(nil)
)
