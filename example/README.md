# Example Site

A minimal site demonstrating `_headers`, `_redirects`, and a Caddyfile using `caddy-static-adapter`.

**`_headers`** — applies response headers by URL pattern:
- Security headers on all pages (`/*`)
- Aggressive caching on `/static/*`
- Placeholder expansion on `/greet/:name`

**`_redirects`** — defines redirect rules:
- Simple path redirects (`/home → /`, `/about-us → /about`)
- Wildcard splat redirect (`/blog/* → /posts/:splat`)
- Placeholder redirect (`/user/:id → /profile/:id`)
- External redirect (`/github → https://github.com`)

**`Caddyfile`** — serves the site on `:8080` with `static_headers` and `static_redirects` enabled.

## Running

Build Caddy with this module, then:

```sh
./caddy run --config example/Caddyfile
```

Try these URLs:
- `http://localhost:8080/` — home page (check security headers in response)
- `http://localhost:8080/about-us` — redirects 301 to `/about`
- `http://localhost:8080/home` — redirects 301 to `/`
- `http://localhost:8080/github` — redirects 302 to `https://github.com`
