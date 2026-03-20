package staticadapter

import (
	pathpkg "path"
	"strings"
)

// isProtectedConfigPath reports whether requestPath resolves to protectedPath
// after the same normalization path.Clean would apply.
//
// The common case returns before calling path.Clean so normal requests do not
// pay for full path normalization on every hit.
// path.Clean on every request gave a perframance hit of 50% in benchmarks
func isProtectedConfigPath(requestPath, protectedPath string) bool {
	if requestPath == protectedPath {
		return true
	}

	if requestPath == "" || protectedPath == "" || protectedPath[0] != '/' {
		return false
	}

	name := protectedPath[1:]
	if name == "" || !strings.Contains(requestPath, name) {
		return false
	}

	if !needsPathClean(requestPath) {
		return false
	}

	return pathpkg.Clean(requestPath) == protectedPath
}

func needsPathClean(requestPath string) bool {
	if requestPath == "" || requestPath[0] != '/' {
		return false
	}

	last := len(requestPath) - 1
	for i := 0; i < len(requestPath); i++ {
		switch requestPath[i] {
		case '/':
			if i > 0 && requestPath[i-1] == '/' {
				return true
			}
			if i == last && i > 0 {
				return true
			}
		case '.':
			if i == 0 || requestPath[i-1] != '/' {
				continue
			}
			if i == last || requestPath[i+1] == '/' {
				return true
			}
			if requestPath[i+1] == '.' && (i+1 == last || requestPath[i+2] == '/') {
				return true
			}
		}
	}

	return false
}
