package staticadapter

import (
	"strings"
	"testing"
)

// ---------- Normalize Path Tests ----------

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "/"},
		{"/", "/"},
		{"/foo/bar", "/foo/bar"},
		{"foo/bar", "/foo/bar"},
	}

	for _, tt := range tests {
		got := normalizePath(tt.input)
		if got != tt.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------- Extract Path Tests ----------

func TestExtractPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/foo/bar", "/foo/bar"},
		{"/static/*", "/static/*"},
		{"https://example.com/path", "/path"},
		{"https://example.com/", "/"},
		{"https://example.com", "/"},
		{"https://example.com/path/*", "/path/*"},
		{"https://:sub.example.com/*", "/*"},
		{"https://:sub.example.com/movies/:title", "/movies/:title"},
		{"https://example.com:8443/path", "/path"},
		{"https://example.com/path?query=1", "/path"},
		{"https://example.com?query=1", "/"},
		{"https://example.com/caf%C3%A9", "/café"},
	}

	for _, tt := range tests {
		got := extractPath(tt.input)
		if got != tt.want {
			t.Errorf("extractPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------- Extract Host Tests ----------

func TestExtractHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/foo/bar", ""},
		{"https://example.com/path", "example.com"},
		{"https://example.com", "example.com"},
		{"https://example.com:8443/path", "example.com"},
		{"https://user@example.com/path", "example.com"},
		{"https://[::1]:8443/path", "::1"},
		{"https://:sub.example.com/*", ":sub.example.com"},
		{"https://:version.:sub.example.com/", ":version.:sub.example.com"},
		{"https://:sub.example.com:8443/", ":sub.example.com"},
	}

	for _, tt := range tests {
		got := extractHost(tt.input)
		if got != tt.want {
			t.Errorf("extractHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------- Match Host Tests ----------

func TestMatchHost(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		want    bool
	}{
		// Exact match.
		{"example.com", "example.com", true},
		{"example.com", "other.com", false},

		// Case-insensitive.
		{"Example.COM", "example.com", true},

		// Strip port from request host.
		{"example.com", "example.com:8080", true},

		// IPv6 with port — must not break.
		{"::1", "[::1]:8080", true},
		{"example.com", "[2001:db8::1]:8080", false},
		{"::1", "[::1]", true},
		{"2001:db8::1", "[2001:DB8:0::1]:443", true},

		// IPv6 colons are not placeholders.
		{"::1", "localhost", false},
		{"::1", "[::2]", false},

		// Placeholders in host.
		{":sub.example.com", "foo.example.com", true},
		{":sub.example.com", "bar.example.com", true},
		{":sub.example.com", "foo.other.com", false},
	}

	for _, tt := range tests {
		got := matchHost(tt.pattern, tt.host)
		if got != tt.want {
			t.Errorf("matchHost(%q, %q) = %v, want %v", tt.pattern, tt.host, got, tt.want)
		}
	}
}

// ---------- Match Pattern Tests ----------

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Exact.
		{"/foo", "/foo", true},
		{"/foo", "/bar", false},

		// Splat.
		{"/static/*", "/static/file.css", true},
		{"/static/*", "/static/sub/dir/file.css", true},
		{"/static/*", "/other/file.css", false},

		// Suffix.
		{"/*.jpg", "/images/photo.jpg", true},
		{"/*.jpg", "/style.css", false},

		// Placeholders.
		{"/movies/:title", "/movies/inception", true},
		{"/movies/:title", "/movies/", true},
		{"/movies/:title", "/movies/a/b", false},
	}

	for _, tt := range tests {
		got := compilePattern(tt.pattern).match(tt.path)
		if got != tt.want {
			t.Errorf("match(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

// ---------- Capture Pattern Tests ----------

func TestPatternCapture(t *testing.T) {
	tests := []struct {
		pattern      string
		path         string
		ok           bool
		splat        string
		placeholders map[string]string
	}{
		{"/:lang/docs/*", "/en/docs/intro/setup", true, "intro/setup", map[string]string{"lang": "en"}},
		{"/:lang/docs/*", "/en/docs/", true, "", map[string]string{"lang": "en"}},
		{"/:lang/docs/*", "/en/blog/intro", false, "", nil},
		{"/:lang/docs/*", "/en/docs", false, "", nil},
		{"/*/:file", "/a/b/c.txt", true, "a/b", map[string]string{"file": "c.txt"}},
		{"/:a/*/:b", "/x/m/n/y", true, "m/n", map[string]string{"a": "x", "b": "y"}},
		{"/:a/*/:b", "/x/y", false, "", nil},
		{"/:lang/img-*.jpg", "/de/img-cat.jpg", true, "cat", map[string]string{"lang": "de"}},
		{"/:lang/img-*.jpg", "/de/img-cat.png", false, "", nil},
		{"/:/docs/*", "/en/docs/x", false, "", nil},
		{"/movies/:title", "/movies/up", true, "", map[string]string{"title": "up"}},
	}

	for _, tt := range tests {
		placeholders, splat, ok := captureMap(tt.pattern, tt.path)
		if ok != tt.ok {
			t.Errorf("capture(%q, %q) ok = %v, want %v", tt.pattern, tt.path, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if splat != tt.splat {
			t.Errorf("capture(%q, %q) splat = %q, want %q", tt.pattern, tt.path, splat, tt.splat)
		}
		if len(placeholders) != len(tt.placeholders) {
			t.Errorf("capture(%q, %q) placeholders = %v, want %v", tt.pattern, tt.path, placeholders, tt.placeholders)
			continue
		}
		for k, v := range tt.placeholders {
			if placeholders[k] != v {
				t.Errorf("capture(%q, %q) placeholder %q = %q, want %q", tt.pattern, tt.path, k, placeholders[k], v)
			}
		}
		if got := compilePattern(tt.pattern).match(tt.path); got != tt.ok {
			t.Errorf("match(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.ok)
		}
	}
}

// referenceMatchSplat is a straightforward matcher for patterns with a '*'
// and no placeholders, used to cross-check compiledPattern.
func referenceMatchSplat(pattern, path string) bool {
	star := strings.Index(pattern, "*")
	prefix, suffix := pattern[:star], pattern[star+1:]
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path[len(prefix):], suffix)
}

// captureMap runs compiledPattern.capture and returns the placeholder
// values keyed by name.
func captureMap(pattern, path string) (map[string]string, string, bool) {
	p := compilePattern(pattern)
	values, splat, ok := p.capture(path)
	if !ok {
		return nil, "", false
	}
	var m map[string]string
	for i, name := range p.names {
		if m == nil {
			m = make(map[string]string)
		}
		m[name] = values[i]
	}
	return m, splat, true
}

// compiledPattern must agree with a plain prefix/suffix match on patterns
// without placeholders.
func TestPatternCaptureMatchesSplat(t *testing.T) {
	patterns := []string{"/*", "/blog/*", "/*.jpg", "/img-*.jpg", "/a*a", "/*/edit", "/static/*/x.js"}
	paths := []string{"/", "/a", "/aa", "/blog", "/blog/", "/blog/a/b", "/x.jpg", "/a/b.jpg",
		"/img-.jpg", "/img-cat.jpg", "/p/edit", "/p/q/edit", "/edit", "/static/v1/x.js", "/static/x.js"}

	for _, pattern := range patterns {
		for _, path := range paths {
			want := referenceMatchSplat(pattern, path)
			_, splat, got := captureMap(pattern, path)
			if got != want {
				t.Errorf("pattern %q path %q: capture ok = %v, reference = %v", pattern, path, got, want)
				continue
			}
			if !got {
				continue
			}
			star := strings.Index(pattern, "*")
			wantSplat := strings.TrimSuffix(path[star:], pattern[star+1:])
			if splat != wantSplat {
				t.Errorf("pattern %q path %q: splat = %q, want %q", pattern, path, splat, wantSplat)
			}
		}
	}
}

func TestPatternMatchAgreesWithCapture(t *testing.T) {
	patterns := []string{"/", "/a", "/:x", "/a/:x", "/:x/:y", "/*", "/a/*", "/*.css", "/:x/*",
		"/*/:x", "/:x/*/:y", "/a/:x/b/*.js", "/img-*.jpg", "/a*a", "/:x/:x"}
	paths := []string{"/", "/a", "/b", "/a/", "/a/b", "/a/b/c", "/x.css", "/a/b.css", "/a/q/b/c.js",
		"/img-.jpg", "/img-cat.jpg", "/aa", "/a/a"}
	for _, pattern := range patterns {
		p := compilePattern(pattern)
		for _, path := range paths {
			_, _, ok := p.capture(path)
			if got := p.match(path); got != ok {
				t.Errorf("pattern %q path %q: match = %v, capture ok = %v", pattern, path, got, ok)
			}
		}
	}
}
