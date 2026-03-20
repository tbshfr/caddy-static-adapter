package staticadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------- Compile / Match Tests ----------

func TestMatchExact(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/secure/page", Ops: []HeaderOp{{Name: "X-Frame-Options", Value: "DENY", Mode: OpSet}}},
	}
	c := Compile(rules)
	ops := c.MatchOrdered("/secure/page", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Name != "X-Frame-Options" {
		t.Errorf("expected X-Frame-Options, got %s", ops[0].Name)
	}
}

func TestMatchNoMatch(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/secure/page", Ops: []HeaderOp{{Name: "X-Test", Value: "val", Mode: OpSet}}},
	}
	c := Compile(rules)
	ops := c.MatchOrdered("/other/page", "")
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops, got %d", len(ops))
	}
}

func TestMatchWildcard(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/static/*", Ops: []HeaderOp{{Name: "Cache-Control", Value: "public", Mode: OpSet}}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/static/image.jpg", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}

	ops = c.MatchOrdered("/static/sub/dir/file.css", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for nested path, got %d", len(ops))
	}

	ops = c.MatchOrdered("/other/file.jpg", "")
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops for non-matching path, got %d", len(ops))
	}
}

func TestMatchWildcardRoot(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/*", Ops: []HeaderOp{{Name: "X-Global", Value: "yes", Mode: OpSet}}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/anything", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}

	ops = c.MatchOrdered("/deep/nested/path", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for deep path, got %d", len(ops))
	}
}

func TestMatchPlaceholder(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/movies/:title", Ops: []HeaderOp{{Name: "X-Movie-Name", Value: `You are watching ":title"`, Mode: OpSet}}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/movies/inception", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Value != `You are watching "inception"` {
		t.Errorf("expected expanded value, got %s", ops[0].Value)
	}
}

func TestMatchMultipleRules(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/secure/page", Ops: []HeaderOp{
			{Name: "X-Frame-Options", Value: "DENY", Mode: OpSet},
			{Name: "X-Content-Type-Options", Value: "nosniff", Mode: OpSet},
		}},
		{Pattern: "/*", Ops: []HeaderOp{
			{Name: "X-Robots-Tag", Value: "noindex", Mode: OpSet},
		}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/secure/page", "")
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}
}

func TestMatchAbsoluteURL(t *testing.T) {
	rules := []*Rule{
		{Pattern: "https://myworker.mysubdomain.workers.dev/*", Ops: []HeaderOp{
			{Name: "X-Robots-Tag", Value: "noindex", Mode: OpSet},
		}},
	}
	c := Compile(rules)

	// Should match when host matches.
	ops := c.MatchOrdered("/home", "myworker.mysubdomain.workers.dev")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for absolute URL pattern with matching host, got %d", len(ops))
	}

	// Should NOT match when host doesn't match.
	ops = c.MatchOrdered("/home", "other.domain.com")
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops for absolute URL pattern with non-matching host, got %d", len(ops))
	}

	// Should NOT match with empty host.
	ops = c.MatchOrdered("/home", "")
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops for absolute URL pattern with empty host, got %d", len(ops))
	}
}

func TestMatchSuffix(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/*.jpg", Ops: []HeaderOp{
			{Name: "Cache-Control", Value: "public", Mode: OpSet},
		}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/images/photo.jpg", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for .jpg match, got %d", len(ops))
	}

	ops = c.MatchOrdered("/style.css", "")
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops for .css file, got %d", len(ops))
	}
}

func TestSplatExpansion(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/assets/*", Ops: []HeaderOp{
			{Name: "X-Path", Value: "asset is :splat", Mode: OpSet},
		}},
	}
	c := Compile(rules)
	ops := c.MatchOrdered("/assets/img/logo.png", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Value != "asset is img/logo.png" {
		t.Errorf("expected splat expansion, got %q", ops[0].Value)
	}
}

func TestSplatExpansionEmpty(t *testing.T) {
	// When wildcard matches an empty string (e.g., /foo/* matching /foo/),
	// :splat should be replaced with empty string, not left as literal ":splat".
	rules := []*Rule{
		{Pattern: "/foo/*", Ops: []HeaderOp{
			{Name: "X-Path", Value: "path is :splat", Mode: OpSet},
		}},
	}
	c := Compile(rules)
	ops := c.MatchOrdered("/foo/", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Value != "path is " {
		t.Errorf("expected empty splat expansion, got %q", ops[0].Value)
	}
}

func TestSplatPatternLiteralColonValueUnchanged(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/assets/*", Ops: []HeaderOp{
			{Name: "X-URL", Value: "https://cdn.example.com/static", Mode: OpSet},
		}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/assets/img/logo.png", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Value != "https://cdn.example.com/static" {
		t.Errorf("expected literal value unchanged, got %q", ops[0].Value)
	}
}

func TestPlaceholderPatternLiteralColonValueUnchanged(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/movies/:title", Ops: []HeaderOp{
			{Name: "X-URL", Value: "https://cdn.example.com/static", Mode: OpSet},
		}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/movies/inception", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Value != "https://cdn.example.com/static" {
		t.Errorf("expected literal value unchanged, got %q", ops[0].Value)
	}
}

func TestPlaceholderPrefixOverlapExpansion(t *testing.T) {
	rules := []*Rule{
		{Pattern: "/params/:a/:ab", Ops: []HeaderOp{
			{Name: "X-Params", Value: "ab=:ab a=:a", Mode: OpSet},
		}},
	}
	c := Compile(rules)

	ops := c.MatchOrdered("/params/x/yz", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Value != "ab=yz a=x" {
		t.Errorf("expected safe expansion for overlapping placeholders, got %q", ops[0].Value)
	}
}

// ---------- Cloudflare Compatibility Tests ----------

func TestCloudflareDocExample(t *testing.T) {
	// Example from the Cloudflare docs.
	input := `# This is a comment
/secure/page
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
  Referrer-Policy: no-referrer
/static/*
  Access-Control-Allow-Origin: *
  X-Robots-Tag: nosnippet
https://myworker.mysubdomain.workers.dev/*
  X-Robots-Tag: noindex
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	c := Compile(rules)

	// Test: /secure/page on custom.domain → X-Frame-Options, X-Content-Type-Options, Referrer-Policy
	ops := c.MatchOrdered("/secure/page", "custom.domain")
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops for /secure/page on custom.domain, got %d", len(ops))
	}

	// Test: /static/image.jpg on custom.domain → Access-Control-Allow-Origin, X-Robots-Tag
	ops = c.MatchOrdered("/static/image.jpg", "custom.domain")
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops for /static/image.jpg, got %d", len(ops))
	}

	// Test: /home on myworker.mysubdomain.workers.dev → matches absolute URL pattern
	ops = c.MatchOrdered("/home", "myworker.mysubdomain.workers.dev")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for /home on matching host, got %d", len(ops))
	}
	if ops[0].Value != "noindex" {
		t.Errorf("expected noindex, got %s", ops[0].Value)
	}

	// Test: /secure/page on myworker.mysubdomain.workers.dev → 3 path-only + 1 absolute URL = 4
	ops = c.MatchOrdered("/secure/page", "myworker.mysubdomain.workers.dev")
	if len(ops) != 4 {
		t.Fatalf("expected 4 ops for /secure/page on workers.dev host, got %d", len(ops))
	}

	// Test: /static/styles.css on myworker.mysubdomain.workers.dev → 2 path-only + 1 absolute URL = 3
	ops = c.MatchOrdered("/static/styles.css", "myworker.mysubdomain.workers.dev")
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops for /static/styles.css on workers.dev host, got %d", len(ops))
	}
}

func TestCloudflareRemoveExample(t *testing.T) {
	input := `/*
  Content-Security-Policy: default-src 'self';
/*.jpg
  ! Content-Security-Policy
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	c := Compile(rules)

	// For /page.html → should get CSP.
	ops := c.MatchOrdered("/page.html", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 op for /page.html, got %d", len(ops))
	}
	if ops[0].Mode != OpSet {
		t.Errorf("expected set op, got %v", ops[0].Mode)
	}

	// For /photo.jpg → should get CSP then remove it.
	ops = c.MatchOrdered("/photo.jpg", "")
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops for /photo.jpg, got %d", len(ops))
	}
	if ops[0].Mode != OpSet {
		t.Errorf("expected first op to be set")
	}
	if ops[1].Mode != OpRemove {
		t.Errorf("expected second op to be remove")
	}
}

func TestCloudflareDuplicateHeaders(t *testing.T) {
	// "If a header is applied twice in the _headers file, the values are joined with a comma separator."
	input := `/static/*
  X-Robots-Tag: nosnippet
/*
  X-Robots-Tag: noindex
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	c := Compile(rules)
	ops := c.MatchOrdered("/static/styles.css", "")

	headers := make(http.Header)
	applyHeaders(headers, ops)

	xRobots := headers.Get("X-Robots-Tag")
	if xRobots != "nosnippet, noindex" {
		t.Errorf("expected comma-joined values, got %q", xRobots)
	}
}

func TestCloudflarePlaceholderExample(t *testing.T) {
	input := `/movies/:title
  x-movie-name: You are watching ":title"
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	c := Compile(rules)
	ops := c.MatchOrdered("/movies/inception", "")

	headers := make(http.Header)
	applyHeaders(headers, ops)

	val := headers.Get("X-Movie-Name")
	if val != `You are watching "inception"` {
		t.Errorf("expected expanded placeholder, got %q", val)
	}
}

// ---------- Apply Headers Tests ----------

func TestApplyHeadersSet(t *testing.T) {
	headers := make(http.Header)
	ops := []HeaderOp{
		{Name: "X-Test", Value: "value1", Mode: OpSet},
	}
	applyHeaders(headers, ops)
	if headers.Get("X-Test") != "value1" {
		t.Errorf("expected value1, got %s", headers.Get("X-Test"))
	}
}

func TestApplyHeadersDuplicate(t *testing.T) {
	headers := make(http.Header)
	ops := []HeaderOp{
		{Name: "X-Test", Value: "value1", Mode: OpSet},
		{Name: "X-Test", Value: "value2", Mode: OpSet},
	}
	applyHeaders(headers, ops)
	if headers.Get("X-Test") != "value1, value2" {
		t.Errorf("expected 'value1, value2', got %s", headers.Get("X-Test"))
	}
}

func TestApplyHeadersRemove(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Test", "existing")
	ops := []HeaderOp{
		{Name: "X-Test", Mode: OpRemove},
	}
	applyHeaders(headers, ops)
	if headers.Get("X-Test") != "" {
		t.Errorf("expected empty after remove, got %s", headers.Get("X-Test"))
	}
}

func TestApplyHeadersSetThenRemove(t *testing.T) {
	headers := make(http.Header)
	ops := []HeaderOp{
		{Name: "Content-Security-Policy", Value: "default-src 'self';", Mode: OpSet},
		{Name: "Content-Security-Policy", Mode: OpRemove},
	}
	applyHeaders(headers, ops)
	if headers.Get("Content-Security-Policy") != "" {
		t.Errorf("expected empty after set+remove, got %s", headers.Get("Content-Security-Policy"))
	}
}

// ---------- Integration Test ----------

func TestServeHTTPIntegration(t *testing.T) {
	input := `# Test headers
/api/*
  X-Api-Version: v1
  Access-Control-Allow-Origin: *
/api/private/*
  X-Requires-Auth: true
/
  X-Home: yes
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	compiled := Compile(rules)

	tests := []struct {
		path    string
		want    map[string]string
		wantNot []string
	}{
		{
			path: "/api/users",
			want: map[string]string{
				"X-Api-Version":               "v1",
				"Access-Control-Allow-Origin": "*",
			},
		},
		{
			path: "/api/private/secrets",
			want: map[string]string{
				"X-Api-Version":               "v1",
				"Access-Control-Allow-Origin": "*",
				"X-Requires-Auth":             "true",
			},
		},
		{
			path: "/",
			want: map[string]string{
				"X-Home": "yes",
			},
			wantNot: []string{"X-Api-Version"},
		},
		{
			path:    "/other",
			want:    map[string]string{},
			wantNot: []string{"X-Api-Version", "X-Home"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ops := compiled.MatchOrdered(tt.path, "")
			applyHeaders(rec.Header(), ops)

			for name, want := range tt.want {
				got := rec.Header().Get(name)
				if got != want {
					t.Errorf("header %s: want %q, got %q", name, want, got)
				}
			}
			for _, name := range tt.wantNot {
				if got := rec.Header().Get(name); got != "" {
					t.Errorf("header %s should not be set, got %q", name, got)
				}
			}
		})
	}
}
