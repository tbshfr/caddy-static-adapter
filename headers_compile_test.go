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

func TestMatchAbsoluteURLHostPlaceholder(t *testing.T) {
	rules, warnings := Parse(strings.NewReader("https://:sub.example.com/*\n  X-Robots-Tag: noindex\n"), 0, 0, 0)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	c := Compile(rules)

	for _, dedup := range []bool{false, true} {
		match := c.MatchOrdered
		if dedup {
			match = c.MatchBySpecificity
		}

		if ops := match("/home", "my-site.example.com"); len(ops) != 1 || ops[0].Value != "noindex" {
			t.Errorf("dedup=%v: expected noindex on matching host, got %v", dedup, ops)
		}
		if ops := match("/home", "example.com"); len(ops) != 0 {
			t.Errorf("dedup=%v: expected no ops on other host, got %v", dedup, ops)
		}
		if ops := match("/home", "a.b.example.com"); len(ops) != 0 {
			t.Errorf("dedup=%v: placeholder must match a single label, got %v", dedup, ops)
		}
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
	applyHeaders(headers, ops, false)

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
	applyHeaders(headers, ops, false)

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
	applyHeaders(headers, ops, false)
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
	applyHeaders(headers, ops, false)
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
	applyHeaders(headers, ops, false)
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
	applyHeaders(headers, ops, false)
	if headers.Get("Content-Security-Policy") != "" {
		t.Errorf("expected empty after set+remove, got %s", headers.Get("Content-Security-Policy"))
	}
}

func TestApplyHeadersDedupOverwrites(t *testing.T) {
	headers := make(http.Header)
	ops := []HeaderOp{
		{Name: "Cache-Control", Value: "public, max-age=31536000, immutable", Mode: OpSet},
		{Name: "Cache-Control", Value: "public, max-age=1, immutable", Mode: OpSet},
		{Name: "Cache-Control", Value: "public, max-age=31536000, immutable", Mode: OpSet},
	}
	applyHeaders(headers, ops, true)
	want := "public, max-age=31536000, immutable"
	if got := headers.Get("Cache-Control"); got != want {
		t.Errorf("dedup: want %q, got %q", want, got)
	}
}

func TestApplyHeadersDedupPreservesDistinct(t *testing.T) {
	headers := make(http.Header)
	ops := []HeaderOp{
		{Name: "X-Frame-Options", Value: "DENY", Mode: OpSet},
		{Name: "X-Content-Type-Options", Value: "nosniff", Mode: OpSet},
	}
	applyHeaders(headers, ops, true)
	if got := headers.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("want DENY, got %q", got)
	}
	if got := headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("want nosniff, got %q", got)
	}
}

func TestApplyHeadersDedupRemoveStillWorks(t *testing.T) {
	headers := make(http.Header)
	ops := []HeaderOp{
		{Name: "Cache-Control", Value: "public, max-age=31536000, immutable", Mode: OpSet},
		{Name: "Cache-Control", Mode: OpRemove},
		{Name: "Cache-Control", Value: "no-cache", Mode: OpSet},
	}
	applyHeaders(headers, ops, true)
	if got := headers.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("dedup with remove: want %q, got %q", "no-cache", got)
	}
}

func TestDedupIntegrationOverlappingWildcards(t *testing.T) {
	input := `/_nuxt/builds/meta/*
  Cache-Control: public, max-age=31536000, immutable
/_nuxt/builds/*
  Cache-Control: public, max-age=1, immutable
/_nuxt/*
  Cache-Control: public, max-age=31536000, immutable
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	compiled := Compile(rules)

	// Without dedup: all three values concatenated in file order
	ops := compiled.MatchOrdered("/_nuxt/builds/meta/abc.json", "")
	headers := make(http.Header)
	applyHeaders(headers, ops, false)
	got := headers.Get("Cache-Control")
	if got != "public, max-age=31536000, immutable, public, max-age=1, immutable, public, max-age=31536000, immutable" {
		t.Errorf("no-dedup: unexpected value %q", got)
	}

	// With dedup + specificity: most specific rule (/_nuxt/builds/meta/*) wins
	ops = compiled.MatchBySpecificity("/_nuxt/builds/meta/abc.json", "")
	headers = make(http.Header)
	applyHeaders(headers, ops, true)
	got = headers.Get("Cache-Control")
	if got != "public, max-age=31536000, immutable" {
		t.Errorf("dedup meta: want %q, got %q", "public, max-age=31536000, immutable", got)
	}

	// With dedup + specificity: /_nuxt/builds/* is more specific than /_nuxt/*
	ops = compiled.MatchBySpecificity("/_nuxt/builds/latest.json", "")
	headers = make(http.Header)
	applyHeaders(headers, ops, true)
	got = headers.Get("Cache-Control")
	if got != "public, max-age=1, immutable" {
		t.Errorf("dedup builds: want %q, got %q", "public, max-age=1, immutable", got)
	}

	// With dedup + specificity: only /_nuxt/* matches
	ops = compiled.MatchBySpecificity("/_nuxt/some-chunk.js", "")
	headers = make(http.Header)
	applyHeaders(headers, ops, true)
	got = headers.Get("Cache-Control")
	if got != "public, max-age=31536000, immutable" {
		t.Errorf("dedup nuxt: want %q, got %q", "public, max-age=31536000, immutable", got)
	}
}

func TestDedupSpecificityExactBeatsWildcard(t *testing.T) {
	input := `/*
  X-Robots-Tag: noindex
/about
  X-Robots-Tag: index, follow
`
	rules, _ := Parse(strings.NewReader(input), 0, 0, 0)
	compiled := Compile(rules)

	ops := compiled.MatchBySpecificity("/about", "")
	headers := make(http.Header)
	applyHeaders(headers, ops, true)
	got := headers.Get("X-Robots-Tag")
	if got != "index, follow" {
		t.Errorf("exact vs wildcard: want %q, got %q", "index, follow", got)
	}
}

func TestDedupSpecificityEqualPreservesFileOrder(t *testing.T) {
	// Two patterns with same prefix length before * — file order wins.
	input := `/assets/*
  Cache-Control: first
/static/*
  Cache-Control: second
`
	rules, _ := Parse(strings.NewReader(input), 0, 0, 0)
	compiled := Compile(rules)

	// /assets/foo only matches first rule
	ops := compiled.MatchBySpecificity("/assets/foo", "")
	headers := make(http.Header)
	applyHeaders(headers, ops, true)
	if got := headers.Get("Cache-Control"); got != "first" {
		t.Errorf("assets: want %q, got %q", "first", got)
	}

	// /static/foo only matches second rule
	ops = compiled.MatchBySpecificity("/static/foo", "")
	headers = make(http.Header)
	applyHeaders(headers, ops, true)
	if got := headers.Get("Cache-Control"); got != "second" {
		t.Errorf("static: want %q, got %q", "second", got)
	}
}

func TestDedupSpecificityMultipleHeaders(t *testing.T) {
	// More specific rule sets Cache-Control; broader rule sets X-Robots-Tag.
	// Both should appear — dedup only overwrites same-name headers.
	input := `/_nuxt/*
  Cache-Control: public, max-age=31536000, immutable
  X-Robots-Tag: noindex
/_nuxt/builds/*
  Cache-Control: public, max-age=1, immutable
`
	rules, _ := Parse(strings.NewReader(input), 0, 0, 0)
	compiled := Compile(rules)

	ops := compiled.MatchBySpecificity("/_nuxt/builds/latest.json", "")
	headers := make(http.Header)
	applyHeaders(headers, ops, true)
	if got := headers.Get("Cache-Control"); got != "public, max-age=1, immutable" {
		t.Errorf("Cache-Control: want %q, got %q", "public, max-age=1, immutable", got)
	}
	if got := headers.Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag: want %q, got %q", "noindex", got)
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
			applyHeaders(rec.Header(), ops, false)

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

func TestDedupKeepsRepeatedHeadersWithinRule(t *testing.T) {
	input := `/*
  Link: </a.css>; rel=preload
  Link: </b.js>; rel=preload
  Cache-Control: no-cache
/blog/:slug
  Cache-Control: public
  Cache-Control: max-age=60
  X-Slug: :slug
  X-Slug: also-:slug
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	compiled := Compile(rules)

	headers := make(http.Header)
	applyHeaders(headers, compiled.MatchBySpecificity("/blog/hello", ""), true)

	if got, want := headers.Get("Link"), "</a.css>; rel=preload, </b.js>; rel=preload"; got != want {
		t.Errorf("Link: want %q, got %q", want, got)
	}
	// /blog/:slug is more specific than /*, so its values replace the
	// /* value but are still joined with each other.
	if got, want := headers.Get("Cache-Control"), "public, max-age=60"; got != want {
		t.Errorf("Cache-Control: want %q, got %q", want, got)
	}
	if got, want := headers.Get("X-Slug"), "hello, also-hello"; got != want {
		t.Errorf("X-Slug: want %q, got %q", want, got)
	}
}

func TestMergeRepeatedHeadersRemoveResets(t *testing.T) {
	ops := []HeaderOp{
		{Name: "X-A", Value: "1", Mode: OpSet},
		{Name: "X-A", Value: "2", Mode: OpSet},
		{Name: "X-A", Mode: OpRemove},
		{Name: "X-A", Value: "3", Mode: OpSet},
		{Name: "X-A", Value: "4", Mode: OpSet},
	}
	headers := make(http.Header)
	applyHeaders(headers, mergeRepeatedHeaders(ops), true)
	if got, want := headers.Get("X-A"), "3, 4"; got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestMatchMixedPlaceholderAndSplat(t *testing.T) {
	rules, warnings := Parse(strings.NewReader("/:lang/blog/*\n  X-Lang: :lang\n  X-Post: :splat\n"), 0, 0, 0)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	compiled := Compile(rules)

	for _, dedup := range []bool{false, true} {
		match := compiled.MatchOrdered
		if dedup {
			match = compiled.MatchBySpecificity
		}
		headers := make(http.Header)
		applyHeaders(headers, match("/de/blog/2024/hello", ""), dedup)
		if got := headers.Get("X-Lang"); got != "de" {
			t.Errorf("dedup=%v: X-Lang: want %q, got %q", dedup, "de", got)
		}
		if got := headers.Get("X-Post"); got != "2024/hello" {
			t.Errorf("dedup=%v: X-Post: want %q, got %q", dedup, "2024/hello", got)
		}
		if ops := match("/de/docs/x", ""); len(ops) != 0 {
			t.Errorf("dedup=%v: expected no ops for /de/docs/x, got %v", dedup, ops)
		}
	}
}

func TestParsePlaceholderSharingSegmentWithSplatRejected(t *testing.T) {
	rules, warnings := Parse(strings.NewReader("/:lang*\n  X-A: 1\n"), 0, 0, 0)
	if len(rules) != 0 || len(warnings) == 0 {
		t.Fatalf("expected pattern to be rejected, got %d rules, warnings %v", len(rules), warnings)
	}
}

func TestDedupSpecificityComparesPatternKinds(t *testing.T) {
	tests := []struct {
		name  string
		input string
		path  string
		want  string
	}{
		{
			name:  "placeholder with longer literal beats short splat",
			input: "/blog/:slug\n  X-Rule: placeholder\n/b*\n  X-Rule: splat\n",
			path:  "/blog/hello",
			want:  "placeholder",
		},
		{
			name:  "longer splat prefix beats placeholder",
			input: "/:section/x\n  X-Rule: placeholder\n/docs/guide/*\n  X-Rule: splat\n",
			path:  "/docs/guide/x",
			want:  "splat",
		},
		{
			name:  "placeholder beats splat at equal literal length",
			input: "/blog/:slug\n  X-Rule: placeholder\n/blog/*\n  X-Rule: splat\n",
			path:  "/blog/hello",
			want:  "placeholder",
		},
		{
			name:  "mixed pattern scored by its literal characters",
			input: "/:lang/docs/*\n  X-Rule: mixed\n/*\n  X-Rule: catch-all\n",
			path:  "/en/docs/intro",
			want:  "mixed",
		},
		{
			name:  "exact beats placeholder",
			input: "/blog/hello\n  X-Rule: exact\n/blog/:slug\n  X-Rule: placeholder\n",
			path:  "/blog/hello",
			want:  "exact",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, warnings := Parse(strings.NewReader(tt.input), 0, 0, 0)
			if len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			// Both file orders must give the same result.
			for _, order := range [][]*Rule{rules, {rules[1], rules[0]}} {
				headers := make(http.Header)
				applyHeaders(headers, Compile(order).MatchBySpecificity(tt.path, ""), true)
				if got := headers.Get("X-Rule"); got != tt.want {
					t.Errorf("first rule %q: want %q, got %q", order[0].Pattern, tt.want, got)
				}
			}
		})
	}
}

func TestHeaderValuesNotReexpanded(t *testing.T) {
	rules, _ := Parse(strings.NewReader("/p/:a/:b\n  X-Pair: :a-:b\n/s/*\n  X-Splat: :splat\n"), 0, 0, 0)
	c := Compile(rules)

	headers := make(http.Header)
	applyHeaders(headers, c.MatchOrdered("/p/:b/real", ""), false)
	if got := headers.Get("X-Pair"); got != ":b-real" {
		t.Errorf("X-Pair: want %q, got %q", ":b-real", got)
	}

	headers = make(http.Header)
	applyHeaders(headers, c.MatchOrdered("/s/:splat", ""), false)
	if got := headers.Get("X-Splat"); got != ":splat" {
		t.Errorf("X-Splat: want %q, got %q", ":splat", got)
	}
}

func TestLiteralColonPatternIsExact(t *testing.T) {
	rules, _ := Parse(strings.NewReader("/a:b\n  X-A: 1\n"), 0, 0, 0)
	c := Compile(rules)
	if len(c.exactPaths) != 1 || len(c.wildcards) != 0 {
		t.Fatalf("expected /a:b to be an exact rule, got %d exact and %d wildcards", len(c.exactPaths), len(c.wildcards))
	}
	if ops := c.MatchOrdered("/a:b", ""); len(ops) != 1 {
		t.Errorf("expected /a:b to match itself, got %v", ops)
	}
}

func TestSetCookieNeverCommaJoined(t *testing.T) {
	input := `/*
  Set-Cookie: site=1; Path=/
/account/*
  Set-Cookie: a=1; Expires=Wed, 21 Oct 2026 07:28:00 GMT
  Set-Cookie: b=2
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	c := Compile(rules)

	headers := make(http.Header)
	applyHeaders(headers, c.MatchOrdered("/account/x", ""), false)
	want := []string{"site=1; Path=/", "a=1; Expires=Wed, 21 Oct 2026 07:28:00 GMT", "b=2"}
	if got := headers.Values("Set-Cookie"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("without dedup: want %q, got %q", want, got)
	}

	// With dedup the more specific rule replaces /* but keeps both of its cookies.
	headers = make(http.Header)
	applyHeaders(headers, c.MatchBySpecificity("/account/x", ""), true)
	want = []string{"a=1; Expires=Wed, 21 Oct 2026 07:28:00 GMT", "b=2"}
	if got := headers.Values("Set-Cookie"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("with dedup: want %q, got %q", want, got)
	}
}

func TestSetCookieRemoveResetsWithinRule(t *testing.T) {
	ops := []HeaderOp{
		{Name: "Set-Cookie", Value: "a=1", Mode: OpSet},
		{Name: "Set-Cookie", Mode: OpRemove},
		{Name: "Set-Cookie", Value: "b=2", Mode: OpSet},
		{Name: "Set-Cookie", Value: "c=3", Mode: OpSet},
	}
	headers := http.Header{"Set-Cookie": {"upstream=1"}}
	applyHeaders(headers, mergeRepeatedHeaders(ops), true)
	if got := headers.Values("Set-Cookie"); strings.Join(got, "|") != "b=2|c=3" {
		t.Errorf("want [b=2 c=3], got %q", got)
	}
}

func TestHeaderValuesEscaped(t *testing.T) {
	rules, _ := Parse(strings.NewReader(`/u/:name
  Set-Cookie: user=:name; Path=/u; HttpOnly
/s/*
  Link: </static/:splat>; rel=preload
  X-Search: /find?q=:splat
`), 0, 0, 0)
	c := Compile(rules)

	headers := make(http.Header)
	applyHeaders(headers, c.MatchOrdered("/u/x; Domain=.example.com", ""), false)
	if got, want := headers.Get("Set-Cookie"), "user=x%3B%20Domain=.example.com; Path=/u; HttpOnly"; got != want {
		t.Errorf("Set-Cookie: want %q, got %q", want, got)
	}

	headers = make(http.Header)
	applyHeaders(headers, c.MatchOrdered("/s/a/b>; rel=x, <https://evil.example", ""), false)
	if got, want := headers.Get("Link"), "</static/a/b%3E%3B%20rel=x%2C%20%3Chttps://evil.example>; rel=preload"; got != want {
		t.Errorf("Link: want %q, got %q", want, got)
	}
	if got, want := headers.Get("X-Search"), "/find?q=a%2Fb%3E%3B+rel%3Dx%2C+%3Chttps%3A%2F%2Fevil.example"; got != want {
		t.Errorf("X-Search: want %q, got %q", want, got)
	}
}
