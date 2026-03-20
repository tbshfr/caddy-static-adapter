package staticadapter

import (
	"testing"
)

// helper to build a compiledRedirect quickly.
func cr(from, to string, status int) *compiledRedirect {
	return &compiledRedirect{
		RedirectRule: &RedirectRule{From: from, To: to, Status: status},
		pathPattern:  from,
	}
}

// ---------- compileRedirects index tests ----------

func TestCompileRedirectsSplitsExactAndWildcard(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/a", "/b", 301),
		cr("/wild/*", "/dest/:splat", 302),
		cr("/c", "/d", 301),
	}
	compiled := compileRedirects(rules)

	if len(compiled.exactRedirects) != 2 {
		t.Errorf("expected 2 exact paths, got %d", len(compiled.exactRedirects))
	}
	if len(compiled.wildcards) != 1 {
		t.Errorf("expected 1 wildcard, got %d", len(compiled.wildcards))
	}
	if compiled.totalRules() != 3 {
		t.Errorf("expected 3 total rules, got %d", compiled.totalRules())
	}
}

func TestCompileRedirectsPlaceholderIsWildcard(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/users/:id", "/profile/:id", 301),
		cr("/exact", "/dest", 301),
	}
	compiled := compileRedirects(rules)

	if len(compiled.wildcards) != 1 {
		t.Errorf("expected placeholder rule in wildcards, got %d", len(compiled.wildcards))
	}
	if len(compiled.exactRedirects) != 1 {
		t.Errorf("expected 1 exact path, got %d", len(compiled.exactRedirects))
	}
}

func TestCompileRedirectsPreservesDefinitionOrder(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/wild/*", "/a/:splat", 301),     // index 0
		cr("/wild/sub/*", "/b/:splat", 302), // index 1
	}
	compiled := compileRedirects(rules)

	if len(compiled.wildcards) != 2 {
		t.Fatalf("expected 2 wildcards, got %d", len(compiled.wildcards))
	}
	if compiled.wildcards[0].index != 0 || compiled.wildcards[1].index != 1 {
		t.Errorf("wildcard indices out of order: %d, %d",
			compiled.wildcards[0].index, compiled.wildcards[1].index)
	}
}

// ---------- MatchFirst interleaving tests ----------

func TestMatchFirstExactOnly(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/a", "/dest-a", 301),
		cr("/b", "/dest-b", 302),
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/a")
	if m == nil || m.To != "/dest-a" {
		t.Fatalf("expected match to /dest-a, got %v", m)
	}

	m = compiled.MatchFirst("/b")
	if m == nil || m.To != "/dest-b" {
		t.Fatalf("expected match to /dest-b, got %v", m)
	}

	m = compiled.MatchFirst("/c")
	if m != nil {
		t.Fatalf("expected no match, got %v", m)
	}
}

func TestMatchFirstWildcardOnly(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/blog/*", "/news/:splat", 301),
		cr("/docs/*", "/help/:splat", 302),
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/blog/post")
	if m == nil || m.To != "/news/:splat" {
		t.Fatalf("expected match to /news/:splat, got %v", m)
	}

	m = compiled.MatchFirst("/docs/faq")
	if m == nil || m.To != "/help/:splat" {
		t.Fatalf("expected match to /help/:splat, got %v", m)
	}

	m = compiled.MatchFirst("/other")
	if m != nil {
		t.Fatalf("expected no match, got %v", m)
	}
}

func TestMatchFirstWildcardBeforeExactWins(t *testing.T) {
	// Wildcard defined first (index 0) should beat exact (index 1)
	// when the wildcard matches the request path.
	rules := []*compiledRedirect{
		cr("/pages/*", "/archive/:splat", 301), // index 0 — wildcard
		cr("/pages/about", "/about-us", 302),   // index 1 — exact
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/pages/about")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/archive/:splat" {
		t.Errorf("expected wildcard (index 0) to win, got %q", m.To)
	}
}

func TestMatchFirstExactBeforeWildcardWins(t *testing.T) {
	// Exact defined first (index 0) should beat wildcard (index 1).
	rules := []*compiledRedirect{
		cr("/pages/about", "/about-us", 301),   // index 0 — exact
		cr("/pages/*", "/archive/:splat", 302), // index 1 — wildcard
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/pages/about")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/about-us" {
		t.Errorf("expected exact (index 0) to win, got %q", m.To)
	}
}

func TestMatchFirstWildcardDoesNotMatchStillUsesExact(t *testing.T) {
	// Wildcard is defined first but does NOT match the request path.
	// The exact match (defined second) should still be returned.
	rules := []*compiledRedirect{
		cr("/other/*", "/elsewhere/:splat", 301), // index 0 — wildcard, won't match
		cr("/target", "/dest", 302),              // index 1 — exact
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/target")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/dest" {
		t.Errorf("expected exact match, got %q", m.To)
	}
}

func TestMatchFirstDuplicateExactFirstDefinedWins(t *testing.T) {
	// Two exact rules for the same path — first one wins.
	rules := []*compiledRedirect{
		cr("/page", "/first", 301),
		cr("/page", "/second", 302),
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/page")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/first" {
		t.Errorf("expected first defined exact to win, got %q", m.To)
	}
	if m.Status != 301 {
		t.Errorf("expected status 301, got %d", m.Status)
	}
}

func TestMatchFirstMultipleWildcardsFirstMatchWins(t *testing.T) {
	// Two wildcards that both match — first one wins.
	rules := []*compiledRedirect{
		cr("/api/*", "/v1/:splat", 301),
		cr("/api/*", "/v2/:splat", 302),
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/api/users")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/v1/:splat" {
		t.Errorf("expected first wildcard to win, got %q", m.To)
	}
}

func TestMatchFirstInterleavedMixedOrder(t *testing.T) {
	// Complex interleaving: exact, wildcard, exact, wildcard
	rules := []*compiledRedirect{
		cr("/a", "/dest-a", 301),           // index 0 — exact
		cr("/b/*", "/dest-b/:splat", 302),  // index 1 — wildcard
		cr("/c", "/dest-c", 301),           // index 2 — exact
		cr("/a/*", "/dest-a2/:splat", 302), // index 3 — wildcard
	}
	compiled := compileRedirects(rules)

	// /a → exact at index 0 wins (wildcard /a/* at index 3 comes after)
	m := compiled.MatchFirst("/a")
	if m == nil || m.To != "/dest-a" {
		t.Errorf("/a: expected /dest-a, got %v", m)
	}

	// /b/x → wildcard at index 1 (only wildcard matching)
	m = compiled.MatchFirst("/b/x")
	if m == nil || m.To != "/dest-b/:splat" {
		t.Errorf("/b/x: expected /dest-b/:splat, got %v", m)
	}

	// /c → exact at index 2 (wildcard at index 1 doesn't match /c)
	m = compiled.MatchFirst("/c")
	if m == nil || m.To != "/dest-c" {
		t.Errorf("/c: expected /dest-c, got %v", m)
	}

	// /a/sub → wildcard at index 3 matches (exact /a at index 0 doesn't match /a/sub)
	m = compiled.MatchFirst("/a/sub")
	if m == nil || m.To != "/dest-a2/:splat" {
		t.Errorf("/a/sub: expected /dest-a2/:splat, got %v", m)
	}
}

func TestMatchFirstPlaceholderBeforeExactWins(t *testing.T) {
	// Placeholder rule (treated as wildcard) defined before exact should win
	// when both match.
	rules := []*compiledRedirect{
		cr("/users/:id", "/profile/:id", 301),   // index 0 — placeholder (wildcard bucket)
		cr("/users/admin", "/admin-panel", 302), // index 1 — exact
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/users/admin")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/profile/:id" {
		t.Errorf("expected placeholder (index 0) to win, got %q", m.To)
	}
}

func TestMatchFirstExactBeforePlaceholderWins(t *testing.T) {
	// Exact defined before placeholder — exact wins.
	rules := []*compiledRedirect{
		cr("/users/admin", "/admin-panel", 301), // index 0 — exact
		cr("/users/:id", "/profile/:id", 302),   // index 1 — placeholder
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/users/admin")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/admin-panel" {
		t.Errorf("expected exact (index 0) to win, got %q", m.To)
	}
}

func TestMatchFirstNoRules(t *testing.T) {
	compiled := compileRedirects(nil)

	m := compiled.MatchFirst("/any")
	if m != nil {
		t.Fatalf("expected no match from empty rules, got %v", m)
	}
}

func TestMatchFirstEarlyTermination(t *testing.T) {
	// The exact match at index 0 should cause MatchFirst to not scan
	// wildcards past index 0. This tests the early termination logic.
	rules := []*compiledRedirect{
		cr("/target", "/dest", 301),              // index 0 — exact
		cr("/other/*", "/elsewhere/:splat", 302), // index 1 — wildcard (won't be reached for /target)
		cr("/*", "/catchall/:splat", 302),        // index 2 — wildcard (won't be reached for /target)
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/target")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.To != "/dest" {
		t.Errorf("expected exact at index 0 to win, got %q", m.To)
	}
}

func TestMatchFirstCatchAllWildcardWithNoExact(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/*", "/home/:splat", 302),
	}
	compiled := compileRedirects(rules)

	m := compiled.MatchFirst("/anything/at/all")
	if m == nil {
		t.Fatal("expected catch-all to match")
	}
	if m.To != "/home/:splat" {
		t.Errorf("expected /home/:splat, got %q", m.To)
	}
}

func TestMatchFirstNormalizesPath(t *testing.T) {
	rules := []*compiledRedirect{
		cr("/path", "/dest", 301),
	}
	compiled := compileRedirects(rules)

	// normalizePath adds a leading "/" to bare paths and converts "" to "/".
	// MatchFirst calls normalizePath, so "path" (no leading slash) should still match "/path".
	m := compiled.MatchFirst("path")
	if m == nil || m.To != "/dest" {
		t.Fatalf("expected match for path without leading slash, got %v", m)
	}
}
