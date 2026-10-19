package staticadapter

import "testing"

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
		got := matchPattern(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}
