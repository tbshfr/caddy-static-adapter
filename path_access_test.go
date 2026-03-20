package staticadapter

import "testing"

func TestIsProtectedConfigPath(t *testing.T) {
	tests := []struct {
		name          string
		requestPath   string
		protectedPath string
		want          bool
	}{
		{name: "exact headers", requestPath: "/_headers", protectedPath: "/_headers", want: true},
		{name: "exact redirects", requestPath: "/_redirects", protectedPath: "/_redirects", want: true},
		{name: "double slash", requestPath: "//_headers", protectedPath: "/_headers", want: true},
		{name: "triple slash", requestPath: "///_redirects", protectedPath: "/_redirects", want: true},
		{name: "traversal", requestPath: "/foo/../_headers", protectedPath: "/_headers", want: true},
		{name: "repeated target traversal", requestPath: "/_redirects/../_redirects", protectedPath: "/_redirects", want: true},
		{name: "trailing slash", requestPath: "/_headers/", protectedPath: "/_headers", want: true},
		{name: "dot segment", requestPath: "/./_headers", protectedPath: "/_headers", want: true},
		{name: "terminal dot", requestPath: "/_redirects/.", protectedPath: "/_redirects", want: true},
		{name: "unrelated page", requestPath: "/page", protectedPath: "/_headers", want: false},
		{name: "different protected file", requestPath: "/_headers", protectedPath: "/_redirects", want: false},
		{name: "nested config file", requestPath: "/assets/_headers", protectedPath: "/_headers", want: false},
		{name: "file extension dot", requestPath: "/site.css", protectedPath: "/_headers", want: false},
		{name: "similar substring", requestPath: "/_headers-backup", protectedPath: "/_headers", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProtectedConfigPath(tt.requestPath, tt.protectedPath); got != tt.want {
				t.Fatalf("isProtectedConfigPath(%q, %q) = %v, want %v", tt.requestPath, tt.protectedPath, got, tt.want)
			}
		})
	}
}
