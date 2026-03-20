package staticadapter

import "github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"

func init() {
	// Register directive order so users do not need to add `order` directives
	// in their Caddyfile. The desired execution order is:
	//   static_redirects → static_headers → header
	//
	// Both are registered "before header" (a standard Caddy directive).
	// Registering static_redirects first places it immediately before header,
	// then registering static_headers before header inserts it between
	// static_redirects and header, yielding the correct order.
	httpcaddyfile.RegisterDirectiveOrder("static_redirects", httpcaddyfile.Before, "header")
	httpcaddyfile.RegisterDirectiveOrder("static_headers", httpcaddyfile.Before, "header")
}
