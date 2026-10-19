package staticadapter

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// normalizePath ensures a URL path has a leading slash for consistent matching.
// Handlers pass the already percent-decoded r.URL.Path, so rules match
// decoded paths (a rule for /café matches a request for /caf%C3%A9), and
// absolute URL patterns are decoded to match (see splitURLPattern). Values
// captured from the path are re-escaped before going into a redirect
// destination or header value (see template.expand).
func normalizePath(path string) string {
	if path == "" {
		return "/"
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

// patternSegment is one '/'-separated part of a compiled pattern: literal
// text, or a :name placeholder that matches any single path segment.
type patternSegment struct {
	literal string
	slot    int // index into the captured values for a placeholder, -1 for literal text
}

func (s patternSegment) match(part string, values []string) bool {
	if s.slot < 0 {
		return part == s.literal
	}
	if values != nil {
		values[s.slot] = part
	}
	return true
}

// compiledPattern is a URL path pattern parsed once at load time, so that
// matching a request neither re-parses the pattern nor allocates.
//
// Without a splat, head holds all segments and a path must have exactly as
// many. With a splat, head holds the segments before the '*', matched from
// the start of the path, and tail those after it, matched from the end;
// before and after are the literal text around the '*' within its own
// segment (e.g. "img-" and ".jpg" in /img-*.jpg). The splat is whatever lies
// in between.
type compiledPattern struct {
	raw      string
	hasSplat bool
	names    []string // placeholder names, indexed by patternSegment.slot
	head     []patternSegment
	tail     []patternSegment
	before   string
	after    string

	// literalLen counts the characters not taken by placeholders or the
	// '*', used to rank dedup specificity.
	literalLen int
}

func compilePattern(raw string) *compiledPattern {
	p := &compiledPattern{raw: raw, literalLen: len(raw)}

	star := strings.IndexByte(raw, '*')
	if star < 0 {
		p.head = p.segments(strings.Split(raw, "/"))
		return p
	}

	p.hasSplat = true
	p.literalLen--
	headParts := strings.Split(raw[:star], "/")
	tailParts := strings.Split(raw[star+1:], "/")
	p.before = headParts[len(headParts)-1]
	p.after = tailParts[0]
	p.head = p.segments(headParts[:len(headParts)-1])
	p.tail = p.segments(tailParts[1:])
	return p
}

// segments compiles whole path segments, registering placeholder names. A
// name used twice shares one slot, so the later segment's value wins.
func (p *compiledPattern) segments(parts []string) []patternSegment {
	segs := make([]patternSegment, len(parts))
	for i, part := range parts {
		segs[i] = patternSegment{literal: part, slot: -1}
		if len(part) < 2 || part[0] != ':' {
			continue
		}
		name := part[1:]
		segs[i].slot = slices.Index(p.names, name)
		if segs[i].slot < 0 {
			segs[i].slot = len(p.names)
			p.names = append(p.names, name)
		}
		p.literalLen -= len(part)
	}
	return segs
}

// exact reports whether the pattern only matches its own literal text.
func (p *compiledPattern) exact() bool {
	return !p.hasSplat && len(p.names) == 0
}

// firstSegment returns the literal first path segment every match must
// have, if the pattern fixes one (e.g. "blog" for /blog/:slug).
func (p *compiledPattern) firstSegment() (string, bool) {
	if len(p.head) < 2 || p.head[0].slot >= 0 || p.head[0].literal != "" || p.head[1].slot >= 0 {
		return "", false
	}
	return p.head[1].literal, true
}

// match reports whether path matches the pattern.
func (p *compiledPattern) match(path string) bool {
	_, _, ok := p.walk(path, nil)
	return ok
}

// capture matches path and returns the placeholder values, indexed like
// p.names, and the splat value.
func (p *compiledPattern) capture(path string) (values []string, splat string, ok bool) {
	if len(p.names) > 0 {
		values = make([]string, len(p.names))
	}
	start, end, ok := p.walk(path, values)
	if !ok {
		return nil, "", false
	}
	if p.hasSplat {
		splat = path[start:end]
	}
	return values, splat, true
}

// walk matches path, storing placeholder values when values is non-nil,
// and returns the bounds of the splat within path.
func (p *compiledPattern) walk(path string, values []string) (start, end int, ok bool) {
	rest := path
	if !p.hasSplat {
		for i, seg := range p.head {
			part := rest
			if i < len(p.head)-1 {
				j := strings.IndexByte(rest, '/')
				if j < 0 {
					return 0, 0, false
				}
				part, rest = rest[:j], rest[j+1:]
			} else if strings.IndexByte(rest, '/') >= 0 {
				return 0, 0, false
			}
			if !seg.match(part, values) {
				return 0, 0, false
			}
		}
		return 0, 0, true
	}

	for _, seg := range p.head {
		j := strings.IndexByte(rest, '/')
		if j < 0 || !seg.match(rest[:j], values) {
			return 0, 0, false
		}
		rest = rest[j+1:]
	}
	start = len(path) - len(rest)

	// Matching the tail only within what the head left keeps both apart.
	for i := len(p.tail) - 1; i >= 0; i-- {
		j := strings.LastIndexByte(rest, '/')
		if j < 0 || !p.tail[i].match(rest[j+1:], values) {
			return 0, 0, false
		}
		rest = rest[:j]
	}
	end = start + len(rest)

	if len(rest) < len(p.before)+len(p.after) ||
		!strings.HasPrefix(rest, p.before) || !strings.HasSuffix(rest, p.after) {
		return 0, 0, false
	}
	return start + len(p.before), end - len(p.after), true
}

// splatSlot marks a template part that refers to :splat.
const splatSlot = -2

// templatePart is literal text (slot -1) or a reference to a captured
// placeholder value or the splat.
type templatePart struct {
	literal string
	slot    int
	query   bool // reference lies in the query string of a URL template
}

// template is a header value or redirect destination whose :name and
// :splat references were resolved against a pattern at load time, so
// expanding it is a single pass. Substituted values are never scanned
// again, so a value containing ":name" is not expanded a second time.
type template struct {
	raw   string
	parts []templatePart // nil when raw has no references
}

func compileTemplate(raw string, p *compiledPattern) template {
	t := template{raw: raw}
	if !strings.Contains(raw, ":") || (!p.hasSplat && len(p.names) == 0) {
		return t
	}

	type token struct {
		text string
		slot int
	}
	var tokens []token
	if p.hasSplat {
		tokens = append(tokens, token{":splat", splatSlot})
	}
	for i, name := range p.names {
		tokens = append(tokens, token{":" + name, i})
	}
	// Longer tokens first, or :id would match inside :idx.
	sort.SliceStable(tokens, func(i, j int) bool { return len(tokens[i].text) > len(tokens[j].text) })

	// A literal '?' starts a query string, which a '#' or a character that
	// can't appear in a URL ends, so that in a header value like
	// Link: </a?v=1>, </b/:slug> the query of one URL doesn't cover the
	// next. Substituted values are escaped, so they can't start one.
	inQuery, inFragment := false, false
	lit := 0
	for i := 0; i < len(raw); {
		matched := false
		if raw[i] == ':' {
			for _, tok := range tokens {
				if strings.HasPrefix(raw[i:], tok.text) {
					if lit < i {
						t.parts = append(t.parts, templatePart{literal: raw[lit:i], slot: -1})
					}
					t.parts = append(t.parts, templatePart{slot: tok.slot, query: inQuery})
					i += len(tok.text)
					lit = i
					matched = true
					break
				}
			}
		}
		if matched {
			continue
		}
		switch raw[i] {
		case '?':
			inQuery = !inFragment
		case '#':
			inQuery, inFragment = false, true
		case ' ', '\t', '"', '<', '>':
			inQuery, inFragment = false, false
		}
		i++
	}
	if t.parts != nil && lit < len(raw) {
		t.parts = append(t.parts, templatePart{literal: raw[lit:], slot: -1})
	}
	return t
}

// join returns t followed by sep and u, each keeping the escaping it was
// compiled with.
func (t template) join(sep string, u template) template {
	j := template{raw: t.raw + sep + u.raw}
	if t.parts == nil && u.parts == nil {
		return j
	}
	j.parts = append(j.parts, t.partsOrLiteral()...)
	j.parts = append(j.parts, templatePart{literal: sep, slot: -1})
	j.parts = append(j.parts, u.partsOrLiteral()...)
	return j
}

func (t template) partsOrLiteral() []templatePart {
	if t.parts == nil {
		return []templatePart{{literal: t.raw, slot: -1}}
	}
	return t.parts
}

// dynamic reports whether the template references the pattern.
func (t template) dynamic() bool {
	return t.parts != nil
}

// expand substitutes captured values, percent-encoded: as a query
// component in the query string, so that a value containing & or = can't
// add parameters, and as a path segment elsewhere. Besides keeping redirect
// destinations on their host, this keeps a value from adding cookie
// attributes (;) or list items (,) to a header, and reproduces the
// still-encoded values Cloudflare inserts, as it matches undecoded paths.
func (t template) expand(values []string, splat string) string {
	if t.parts == nil {
		return t.raw
	}
	var b strings.Builder
	b.Grow(len(t.raw) + len(splat))
	for _, part := range t.parts {
		switch {
		case part.slot == -1:
			b.WriteString(part.literal)
		case part.query && part.slot == splatSlot:
			b.WriteString(url.QueryEscape(splat))
		case part.query:
			b.WriteString(url.QueryEscape(values[part.slot]))
		case part.slot == splatSlot:
			b.WriteString(escapeSplat(splat))
		default:
			b.WriteString(url.PathEscape(values[part.slot]))
		}
	}
	return b.String()
}

// escapeSplat percent-encodes each segment of a splat value while keeping
// the '/' separators intact.
func escapeSplat(splat string) string {
	segments := strings.Split(splat, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// validatePlaceholderSegments reports an error when a placeholder shares a
// path segment with the splat (e.g. /:lang*), which can't be matched
// unambiguously.
func validatePlaceholderSegments(pattern string, lineNum int) error {
	for _, seg := range strings.Split(pattern, "/") {
		if strings.HasPrefix(seg, ":") && strings.Contains(seg, "*") {
			return &ParseError{Line: lineNum, Message: fmt.Sprintf("placeholder and wildcard (*) must be in separate path segments: %q", seg)}
		}
	}
	return nil
}

// matchHost checks if a request host matches a pattern host.
// The pattern host may contain placeholders like :version and :subdomain.
func matchHost(patternHost, requestHost string) bool {
	// Strip port from request host if present, using net.SplitHostPort
	// which correctly handles IPv6 addresses like [::1]:8080.
	if h, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = h
	}

	// The colons of an IPv6 address would otherwise read as placeholders,
	// letting https://[::1]/ match any host without a dot.
	if ip := net.ParseIP(patternHost); ip != nil {
		return ip.Equal(net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(requestHost, "["), "]")))
	}

	if !strings.Contains(patternHost, ":") {
		return strings.EqualFold(patternHost, requestHost)
	}

	// Handle placeholders in host.
	patternParts := strings.Split(patternHost, ".")
	requestParts := strings.Split(requestHost, ".")
	if len(patternParts) != len(requestParts) {
		return false
	}
	for i, pp := range patternParts {
		if strings.HasPrefix(pp, ":") {
			continue // placeholder matches any segment
		}
		if !strings.EqualFold(pp, requestParts[i]) {
			return false
		}
	}
	return true
}

// extractPath extracts the path portion from a pattern.
// For absolute URLs like "https://example.com/path", it extracts "/path".
// For relative patterns like "/path/*", it returns them as-is.
func extractPath(pattern string) string {
	if !strings.HasPrefix(pattern, "https://") {
		return pattern
	}
	_, path := splitURLPattern(pattern)
	return path
}

// extractHost extracts the host portion from an absolute URL pattern.
// Returns empty string for relative path patterns.
func extractHost(pattern string) string {
	if !strings.HasPrefix(pattern, "https://") {
		return ""
	}
	host, _ := splitURLPattern(pattern)
	return host
}

// splitURLPattern splits an absolute https:// pattern into its host (without
// userinfo or port, as Cloudflare ignores ports) and its decoded path.
//
// url.Parse can't be used here: it rejects host placeholders such as
// https://:sub.example.com/* because it reads ":sub.example.com" as an
// invalid port, which silently dropped the host constraint.
func splitURLPattern(pattern string) (host, path string) {
	rest := strings.TrimPrefix(pattern, "https://")

	authority, path := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		path = "/"
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}

	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	return stripPatternPort(authority), path
}

// stripPatternPort removes a trailing :port from a pattern host. A colon
// followed by anything other than digits is a placeholder, not a port.
func stripPatternPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end > 0 {
			return host[1:end]
		}
		return host
	}

	i := strings.LastIndexByte(host, ':')
	if i < 0 {
		return host
	}
	for _, c := range host[i+1:] {
		if c < '0' || c > '9' {
			return host
		}
	}
	return host[:i]
}
