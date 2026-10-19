package staticadapter

import (
	"net"
	"net/url"
	"strings"
)

// normalizePath ensures a URL path has a leading slash for consistent matching.
// No percent-decoding is performed — we match on the raw path as Cloudflare does.
func normalizePath(path string) string {
	if path == "" {
		return "/"
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

// hasWildcardOrPlaceholder returns true if the pattern contains wildcards or placeholders.
func hasWildcardOrPlaceholder(pattern string) bool {
	return strings.Contains(pattern, "*") || strings.Contains(pattern, ":")
}

// matchPattern matches a request path against a pattern that may contain
// a splat (*) or named placeholders (:name).
func matchPattern(pattern, path string) bool {
	// Handle splat wildcard.
	if strings.Contains(pattern, "*") {
		return matchSplat(pattern, path)
	}

	// Handle placeholders.
	if strings.Contains(pattern, ":") {
		return matchPlaceholders(pattern, path)
	}

	return pattern == path
}

// matchSplat matches a path against a pattern containing a single *.
// The * greedily matches all characters.
func matchSplat(pattern, path string) bool {
	starIdx := strings.Index(pattern, "*")
	prefix := pattern[:starIdx]
	suffix := pattern[starIdx+1:]

	if !strings.HasPrefix(path, prefix) {
		return false
	}

	if suffix == "" {
		return true
	}

	remaining := path[len(prefix):]
	return strings.HasSuffix(remaining, suffix)
}

// matchPlaceholders matches a path against a pattern with named placeholders.
// Placeholders match all characters except '/'.
func matchPlaceholders(pattern, path string) bool {
	_, ok := extractPlaceholders(pattern, path)
	return ok
}

// extractPlaceholders extracts placeholder values from a path given a pattern.
// Returns the map of placeholder names to values, and whether the match succeeded.
func extractPlaceholders(pattern, path string) (map[string]string, bool) {
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(patternParts) != len(pathParts) {
		return nil, false
	}

	result := make(map[string]string)
	for i, pp := range patternParts {
		if strings.HasPrefix(pp, ":") {
			// This is a placeholder - extract value.
			name := pp[1:]
			if name == "" {
				return nil, false
			}
			result[name] = pathParts[i]
		} else if pp != pathParts[i] {
			return nil, false
		}
	}

	return result, true
}

// matchHost checks if a request host matches a pattern host.
// The pattern host may contain placeholders like :version and :subdomain.
func matchHost(patternHost, requestHost string) bool {
	// Strip port from request host if present, using net.SplitHostPort
	// which correctly handles IPv6 addresses like [::1]:8080.
	if h, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = h
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
