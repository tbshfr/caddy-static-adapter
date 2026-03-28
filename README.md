# caddy-static-adapter

A [Caddy](https://caddyserver.com/) HTTP handler module that reads and applies headers and redirects from Cloudflare Pages / Netlify-compatible `_headers` and `_redirects` files.

> [!NOTE]
> This module was mostly built using AI

## What it does

- Reads `_headers` and `_redirects` from your site root automatically (follows the `root` directive)
- Watches for file changes using filesystem notifications (falls back to polling if unavailable) - rebuild your site and the new rules are picked up instantly
- Understands the same syntax as [Cloudflare Pages `_headers`](https://developers.cloudflare.com/workers/static-assets/headers/) and [Cloudflare Pages `_redirects`](https://developers.cloudflare.com/pages/configuration/redirects/) / [Netlify `_redirects`](https://docs.netlify.com/manage/routing/redirects/redirect-options/)
- Supports wildcards (`*`), named placeholders (`:name`), splat expansion (`:splat`), header removal (`! Header-Name`), and absolute URL patterns
- Blocks direct access to `/_headers` and `/_redirects` (returns 404) so your config files aren't exposed
- Validates header names, rejects CRLF injection attempts, and enforces file size / rule count limits
- Works with both Caddyfile and JSON config

## Installation

```bash
xcaddy build --with github.com/tbshfr/caddy-static-adapter
```

## Quick start

```caddyfile
:8080 {
    root * /var/www
    file_server
    static_headers
    static_redirects
}
```

That's it. Both directives pick up files from the `root` directory. The module registers itself in the right execution order (`static_redirects` -> `static_headers` -> `header`), so you don't need a global `order` directive.

## `_headers`

Place a `_headers` file in your site root. Format follows the [Cloudflare Pages spec](https://developers.cloudflare.com/workers/static-assets/headers/):

```
# Set security headers on a specific page
/secure/page
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
  Referrer-Policy: no-referrer

# Cache static assets
/static/*
  Access-Control-Allow-Origin: *
  Cache-Control: public, max-age=31556952, immutable

# Remove a header for all JPEGs
/*.jpg
  ! Content-Security-Policy

# Use named placeholders in values
/movies/:title
  x-movie-name: You are watching ":title"
```

### Syntax

Each block starts with an unindented URL pattern, followed by indented header lines:

- `Name: value` — sets a header. If the same header matches from multiple rules, values are comma-joined (Cloudflare semantics).
- `! Name` — removes a header entirely.
- `#` lines are comments.
- `*` is a wildcard (greedy, one per pattern). Use `:splat` in values to reference what it matched.
- `:name` placeholders match a single path segment. Use `:name` in values to expand them.
- Patterns starting with `https://` match against both host and path.
- All matching rules are applied, in file order.

### Limits

| | Default |
|---|---|
| Max rules | 100 |
| Max line length | 2,000 chars |
| Max file size | 1 MB |

## `_redirects`

Place a `_redirects` file in your site root:

```
# Simple redirects
/home              /index.html
/blog/old-post     /blog/new-post     301
/news              /blog              302
/google            https://www.google.com  301

# Wildcards with splat
/blog/*            /news/:splat       301
/docs/*            https://docs.example.com/:splat  301

# Named placeholders
/products/:code/:name  /items?code=:code&name=:name  301
```

### Syntax

Each line is `/from /to [status]`:

- **Source** must start with `/`. Supports `*` and `:name`.
- **Destination** must start with `/` or be an absolute URL (`https://` or `http://`).
- **Status** is optional, defaults to 302. Supported: 301, 302, 303, 307, 308.
- `#` lines are comments.
- **First match wins** — unlike `_headers` where all matches apply, redirects stop at the first match.

### Limits

| | Default |
|---|---|
| Max rules | 2,000 |
| Max line length | 1,000 chars |
| Max file size | 1 MB |

### What's not supported

Some Netlify/Cloudflare features don't map well to Caddy and aren't implemented. Use Caddy's native directives instead:

| Feature | Alternative |
|---|---|
| Rewrites (status 200) | `rewrite` directive |
| Custom 404/410 pages | `handle_errors` directive |
| Proxying | `reverse_proxy` directive |
| Query string matching | - |
| Forced redirects (`!` suffix) | - |
| Country/language/role-based redirects | - |
| Absolute URLs in source path | - |

## Auto-reload

Both handlers use filesystem notifications (`fsnotify`) to watch for changes. When a file is modified - for example during a site rebuild - the new rules are loaded automatically with a short debounce to avoid reacting to intermediate file states. No Caddy restart needed.

If filesystem notifications aren't available, the handlers fall back to polling every 2 seconds.

If a file is missing, the handler just serves requests without applying any rules and picks the file up once it appears.

## Configuration

### Caddyfile options

`static_headers` and `static_redirects` accept the following options:

```caddyfile
static_headers {
    strict
    dedup
    max_rules 100
    max_file_size 1048576
    max_line_length 2000
}
```

```caddyfile
static_redirects {
    strict
    max_rules 2000
    max_file_size 1048576
    max_line_length 1000
}
```

| Option | Description |
|---|---|
| `strict` | Any parse warning discards **all** rules (default: log warnings, keep valid rules) |
| `dedup` | When the same header is set by multiple matching rules, the most specific pattern wins instead of comma-joining (default: off). Rules are ordered from least to most specific, and the final (most specific) matching rule overwrites earlier ones. Longer wildcard prefixes and exact matches are considered more specific than broader patterns. Useful for `_headers` files with overlapping wildcard rules. Only applies to `static_headers`. |
| `max_rules` | Max number of rules. Default: 100 for headers, 2000 for redirects |
| `max_file_size` | Max file size in bytes (default: 1048576) |
| `max_line_length` | Max characters per line. Default: 2000 for headers, 1000 for redirects |

### JSON config

```json
{
  "handler": "static_headers",
  "strict": false,
  "dedup": false,
  "max_rules": 100,
  "max_file_size": 1048576,
  "max_line_length": 2000
}
```

```json
{
  "handler": "static_redirects",
  "strict": false,
  "max_rules": 2000,
  "max_file_size": 1048576,
  "max_line_length": 1000
}
```

## Dev build

```bash
xcaddy build --with github.com/tbshfr/caddy-static-adapter=./
```
## Why does this exist? 

I have some sites built with [Nuxt](https://nuxt.com/) - the [Netlify](https://nitro.build/deploy/providers/netlify) / [Cloudflare Pages](https://nitro.build/deploy/providers/cloudflare#cloudflare-pages) presets generate `_headers` and `_redirects` files with CSP ([nuxt-security](https://nuxt-security.vercel.app/)) and other headers automatically. This module makes it easy to host those static sites with Caddy and still use the auto generated headers and redirects.

## License

See [LICENSE](LICENSE) for details.
