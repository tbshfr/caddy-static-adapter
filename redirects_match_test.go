package staticadapter

import (
	"strings"
	"testing"
)

func TestRedirectMatchExact(t *testing.T) {
	input := `/old /new 301
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)

	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	// Should match.
	requestPath := normalizePath("/old")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "/new" {
				t.Errorf("expected /new, got %s", to)
			}
			return
		}
	}
	t.Fatal("expected match for /old")
}

func TestRedirectMatchWildcard(t *testing.T) {
	input := `/blog/* /news/:splat 301
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	requestPath := normalizePath("/blog/hello-world")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "/news/hello-world" {
				t.Errorf("expected /news/hello-world, got %s", to)
			}
			return
		}
	}
	t.Fatal("expected match for /blog/hello-world")
}

func TestRedirectMatchWildcardDeepPath(t *testing.T) {
	input := `/blog/* /news/:splat 301
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	requestPath := normalizePath("/blog/2024/01/post-title")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "/news/2024/01/post-title" {
				t.Errorf("expected /news/2024/01/post-title, got %s", to)
			}
			return
		}
	}
	t.Fatal("expected match for /blog/2024/01/post-title")
}

func TestRedirectMatchPlaceholders(t *testing.T) {
	input := `/products/:code/:name /items/:code/:name 301
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	requestPath := normalizePath("/products/abc/widget")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "/items/abc/widget" {
				t.Errorf("expected /items/abc/widget, got %s", to)
			}
			return
		}
	}
	t.Fatal("expected match for /products/abc/widget")
}

func TestRedirectNoMatch(t *testing.T) {
	input := `/old /new 301
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	requestPath := normalizePath("/other")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			t.Fatal("expected no match for /other")
		}
	}
}

func TestRedirectFirstMatchWins(t *testing.T) {
	input := `/page /first 301
/page /second 302
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	requestPath := normalizePath("/page")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "/first" {
				t.Errorf("expected first match (/first), got %s", to)
			}
			if cr.Status != 301 {
				t.Errorf("expected status 301, got %d", cr.Status)
			}
			return
		}
	}
	t.Fatal("expected match for /page")
}

func TestRedirectExpandSplatToAbsolute(t *testing.T) {
	input := `/docs/* https://docs.example.com/:splat 301
`
	rules, _ := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	requestPath := normalizePath("/docs/getting-started")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "https://docs.example.com/getting-started" {
				t.Errorf("expected https://docs.example.com/getting-started, got %s", to)
			}
			return
		}
	}
	t.Fatal("expected match for /docs/getting-started")
}

// ---------- Cloudflare/Netlify Compatibility Tests ----------

func TestRedirectCloudflareExample(t *testing.T) {
	// Example from Cloudflare Pages docs.
	input := `/home301 /home.html 301
/home302 /home.html 302
/querystrings /home.html 301
/trailing /home.html 301
/nocode /home.html
/blog/* https://blog.example.com/:splat
/products/:code/:name /products?code=:code&name=:name
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 7 {
		t.Fatalf("expected 7 rules, got %d", len(rules))
	}

	// Verify specific rules.
	if rules[0].Status != 301 {
		t.Errorf("rule 0: expected 301, got %d", rules[0].Status)
	}
	if rules[4].Status != 302 {
		t.Errorf("rule 4 (nocode): expected default 302, got %d", rules[4].Status)
	}

	// Test wildcard redirect.
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}

	// /blog/my-post → https://blog.example.com/my-post
	requestPath := normalizePath("/blog/my-post")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "https://blog.example.com/my-post" {
				t.Errorf("expected https://blog.example.com/my-post, got %s", to)
			}
			break
		}
	}

	// /products/42/widget → /products?code=42&name=widget
	requestPath = normalizePath("/products/42/widget")
	for _, cr := range compiled {
		if cr.pattern.match(requestPath) {
			to := cr.expand(requestPath)
			if to != "/products?code=42&name=widget" {
				t.Errorf("expected /products?code=42&name=widget, got %s", to)
			}
			break
		}
	}
}

func TestRedirectMatch404RuleSkipped(t *testing.T) {
	input := `/old /new 301
/* /404.html 404
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	// The 404 rule should be skipped with a warning.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule (404 skipped), got %d", len(rules))
	}

	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}
	cr := compileRedirects(compiled)

	// /old should still match the 301 redirect.
	match := cr.MatchFirst("/old")
	if match == nil {
		t.Fatal("expected match for /old")
	}
	if match.Status != 301 {
		t.Errorf("expected status 301 for /old, got %d", match.Status)
	}

	// /anything-else should have no match since 404 was skipped.
	match = cr.MatchFirst("/anything-else")
	if match != nil {
		t.Error("expected no match for /anything-else (404 rule should be skipped)")
	}
}

func TestExpandRedirectToPlaceholderPrefixes(t *testing.T) {
	// Repeat to catch map iteration order dependence.
	for i := 0; i < 50; i++ {
		to := newCompiledRedirect(&RedirectRule{From: "/p/:id/:idx", To: "/item?a=:id&b=:idx"}).expand("/p/AAA/BBB")
		if to != "/item?a=AAA&b=BBB" {
			t.Fatalf("iteration %d: expected /item?a=AAA&b=BBB, got %s", i, to)
		}
	}
}

func TestExpandRedirectToValuesNotReexpanded(t *testing.T) {
	for i := 0; i < 50; i++ {
		to := newCompiledRedirect(&RedirectRule{From: "/p/:a/:b", To: "/x/:a/:b"}).expand("/p/:b/real")
		if to != "/x/:b/real" {
			t.Fatalf("iteration %d: expected /x/:b/real, got %s", i, to)
		}
	}
}

func TestRedirectMixedPlaceholderAndSplat(t *testing.T) {
	rules, warnings := ParseRedirects(strings.NewReader("/:lang/docs/* /docs/:lang/:splat 301\n"), 0, 0, 0)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	compiled := make([]*compiledRedirect, len(rules))
	for i, r := range rules {
		compiled[i] = newCompiledRedirect(r)
	}
	cr := compileRedirects(compiled)

	match := cr.MatchFirst("/en/docs/intro/setup")
	if match == nil {
		t.Fatal("expected match for /en/docs/intro/setup")
	}
	if to := match.expand("/en/docs/intro/setup"); to != "/docs/en/intro/setup" {
		t.Errorf("expected /docs/en/intro/setup, got %s", to)
	}
	if cr.MatchFirst("/en/blog/intro") != nil {
		t.Error("expected no match for /en/blog/intro")
	}
}

func TestRedirectPlaceholderSharingSegmentWithSplatRejected(t *testing.T) {
	rules, warnings := ParseRedirects(strings.NewReader("/:lang* /x 301\n"), 0, 0, 0)
	if len(rules) != 0 || len(warnings) != 1 {
		t.Fatalf("expected rule to be rejected with 1 warning, got %d rules, warnings %v", len(rules), warnings)
	}
}

func TestRedirectQueryPlaceholdersEscaped(t *testing.T) {
	tests := []struct {
		from, to, path, want string
	}{
		{"/products/:code", "/items?code=:code&x=1", "/products/a&admin=1", "/items?code=a%26admin%3D1&x=1"},
		{"/s/*", "/search?q=:splat", "/s/a/b c", "/search?q=a%2Fb+c"},
		{"/v/:v", "/x/:v?y=:v", "/v/a b&c", "/x/a%20b&c?y=a+b%26c"},
		{"/v/:v", "/x#:v?:v", "/v/a&b", "/x#a&b?a&b"},
		{"/v/:v", "https://example.com/:v?ref=:v#top", "/v/a=b", "https://example.com/a=b?ref=a%3Db#top"},
	}
	for _, tt := range tests {
		got := newCompiledRedirect(&RedirectRule{From: tt.from, To: tt.to}).expand(tt.path)
		if got != tt.want {
			t.Errorf("%s -> %s for %s: want %s, got %s", tt.from, tt.to, tt.path, tt.want, got)
		}
	}
}
