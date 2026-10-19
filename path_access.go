package staticadapter

import (
	pathpkg "path"
	"strings"
)

// isProtectedConfigPath reports whether requestPath could resolve to the
// file at protectedPath on any filesystem Caddy may serve from.
//
// Besides path.Clean normalization this covers case-insensitive filesystems
// (macOS, Windows: /_HEADERS, including non-ASCII variants such as ſ, U+017F,
// for s) and Windows path quirks (backslash separators, trailing dots and
// spaces: /_headers.). Caddy's file_server already rejects NTFS alternate
// data streams and 8.3 short names on Windows.
//
// The common case returns before any normalization so normal requests do not
// pay for it on every hit; path.Clean on every request gave a performance hit
// of 50% in benchmarks.
func isProtectedConfigPath(requestPath, protectedPath string) bool {
	if requestPath == protectedPath {
		return true
	}

	if requestPath == "" || protectedPath == "" || protectedPath[0] != '/' {
		return false
	}

	name := protectedPath[1:]
	if name == "" || strings.IndexByte(requestPath, name[0]) < 0 {
		return false
	}
	// containsFold compares byte windows as long as name, so it would miss
	// case variants encoded in more bytes (ſ is two). Non-ASCII paths are
	// rare enough to always take the full check.
	if isASCII(requestPath) && !containsFold(requestPath, name) {
		return false
	}

	segments := strings.Split(strings.ReplaceAll(requestPath, `\`, "/"), "/")
	for i, s := range segments {
		if t := strings.TrimRight(s, " "); t == "." || t == ".." {
			segments[i] = t
			continue
		}
		segments[i] = strings.TrimRight(s, ". ")
	}

	cleaned := pathpkg.Clean(strings.Join(segments, "/"))
	// Filesystems fold case differently: APFS folds K (U+212A, Kelvin) to k,
	// which only EqualFold matches, while NTFS upper-cases ı (U+0131) to I,
	// which only ToUpper matches.
	return strings.EqualFold(cleaned, protectedPath) ||
		strings.ToUpper(cleaned) == strings.ToUpper(protectedPath)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// containsFold reports whether substr is within s, ignoring case.
//
// It jumps between occurrences of substr's first byte with IndexByte instead
// of comparing at every offset, which keeps the cost for normal request paths
// close to strings.Contains. substr must start with a non-letter (both
// protected names start with '_'), so its first byte has no case variants.
// Only exact for ASCII s; see isProtectedConfigPath.
func containsFold(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		j := strings.IndexByte(s[i:], substr[0])
		if j < 0 {
			return false
		}
		i += j
		if i+len(substr) <= len(s) && strings.EqualFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}
