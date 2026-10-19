package staticadapter

import (
	"fmt"
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

// compiledRedirect holds a parsed redirect rule with its compiled source
// pattern and destination.
type compiledRedirect struct {
	*RedirectRule
	pattern *compiledPattern
	to      template
}

func newCompiledRedirect(r *RedirectRule) *compiledRedirect {
	p := compilePattern(r.From)
	return &compiledRedirect{RedirectRule: r, pattern: p, to: compileTemplate(r.To, p)}
}

// expand returns the redirect destination for requestPath, which must match
// the rule.
//
// requestPath is the decoded r.URL.Path, so expanded values are re-escaped
// before insertion. Otherwise a request like /go/%5Cevil.com or /to/evil.com%23
// would put a literal \ or # into the Location header and send the client to
// another host.
func (cr *compiledRedirect) expand(requestPath string) string {
	if !cr.to.dynamic() {
		return cr.To
	}
	values, splat, ok := cr.pattern.capture(requestPath)
	if !ok {
		return cr.To
	}
	to := cr.to.expand(values, splat)

	// A destination like /:splat expanded from /go//evil.com becomes
	// //evil.com, which browsers treat as protocol-relative. Only collapse
	// when expansion introduced it, so an explicit //host destination in
	// the _redirects file keeps working.
	if strings.HasPrefix(to, "//") && !strings.HasPrefix(cr.To, "//") {
		to = "/" + strings.TrimLeft(to, "/")
	}
	return to
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

	// byFirstSegment indexes wildcards by the literal first path segment
	// they require (e.g. "blog" for /blog/:slug), so a request only checks
	// rules that can match it; anyFirstSegment holds the rest. Both keep
	// definition order.
	byFirstSegment  map[string][]indexedRedirect
	anyFirstSegment []indexedRedirect
}

// compileRedirects creates an indexed CompiledRedirects from a list of
// compiled redirect rules.
func compileRedirects(rules []*compiledRedirect) *CompiledRedirects {
	cr := &CompiledRedirects{
		exactRedirects: make(map[string][]indexedRedirect, len(rules)),
		byFirstSegment: make(map[string][]indexedRedirect),
	}
	for i, r := range rules {
		ir := indexedRedirect{index: i, cr: r}
		if r.pattern.exact() {
			cr.exactRedirects[r.pattern.raw] = append(cr.exactRedirects[r.pattern.raw], ir)
			continue
		}
		cr.wildcards = append(cr.wildcards, ir)
		if first, ok := r.pattern.firstSegment(); ok {
			cr.byFirstSegment[first] = append(cr.byFirstSegment[first], ir)
		} else {
			cr.anyFirstSegment = append(cr.anyFirstSegment, ir)
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

	// Merge the two candidate buckets in definition order, looking for a
	// match with a lower index than the best exact.
	a := cr.byFirstSegment[firstPathSegment(requestPath)]
	b := cr.anyFirstSegment
	for len(a) > 0 || len(b) > 0 {
		var ir indexedRedirect
		if len(b) == 0 || (len(a) > 0 && a[0].index < b[0].index) {
			ir, a = a[0], a[1:]
		} else {
			ir, b = b[0], b[1:]
		}
		// If we already have an exact match and this wildcard comes after it,
		// stop — no wildcard can beat the exact match.
		if bestExact != nil && ir.index > bestExact.index {
			break
		}
		if ir.cr.pattern.match(requestPath) {
			return ir.cr
		}
	}

	if bestExact != nil {
		return bestExact.cr
	}

	return nil
}

// firstPathSegment returns the first segment of a path starting with '/'.
func firstPathSegment(path string) string {
	rest := path[1:]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
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
			to := match.expand(normalizePath(r.URL.Path))
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
func (h *RedirectHandler) loadRedirectFile(filePath string) (any, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	parsed, warnings := ParseRedirects(f, h.MaxRules, h.MaxFileSize, h.MaxLineLength)
	if h.Strict {
		if err := firstStrictError(warnings); err != nil {
			return nil, fmt.Errorf("parse error (strict mode): %w", err)
		}
	}
	for _, w := range warnings {
		h.logger.Warn(h.logPrefix+": parse warning", zap.Error(w))
	}

	rules := make([]*compiledRedirect, len(parsed))
	for i, r := range parsed {
		rules[i] = newCompiledRedirect(r)
	}

	compiled := compileRedirects(rules)

	h.logger.Info(h.logPrefix+": loaded redirect rules",
		zap.String("file", filePath),
		zap.Int("total_rules", len(rules)))
	return compiled, nil
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
