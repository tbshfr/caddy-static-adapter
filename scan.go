package staticadapter

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// scanLines reads r line by line, enforcing the file size and line length
// limits shared by the _headers and _redirects parsers, and calls fn for
// every line within them. Limit violations are appended to warnings in line
// order. fn returns true to stop scanning.
//
// bufio.Scanner is not used because it aborts the whole scan when a line
// exceeds its buffer, silently dropping all following rules; here an
// overlong line is skipped like any other invalid line. Memory stays bounded
// because at most maxFileSize+1 bytes are ever read.
func scanLines(r io.Reader, maxFileSize, maxLineLength int, warnings *[]error, fn func(lineNum int, line string) bool) {
	br := bufio.NewReader(io.LimitReader(r, int64(maxFileSize)+1))
	lineNum := 0
	totalBytes := 0

	for {
		raw, err := br.ReadString('\n')
		if raw == "" {
			if err != nil && err != io.EOF {
				*warnings = append(*warnings, fmt.Errorf("read error: %w", err))
			}
			return
		}

		lineNum++
		totalBytes += len(raw)
		if totalBytes > maxFileSize {
			*warnings = append(*warnings, &ParseError{Line: lineNum, Message: fmt.Sprintf("file exceeds maximum size of %d bytes", maxFileSize)})
			return
		}

		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if len(line) > maxLineLength {
			*warnings = append(*warnings, &ParseError{Line: lineNum, Message: fmt.Sprintf("line exceeds maximum length of %d characters", maxLineLength)})
		} else if fn(lineNum, line) {
			return
		}

		if err != nil {
			if err != io.EOF {
				*warnings = append(*warnings, fmt.Errorf("read error: %w", err))
			}
			return
		}
	}
}
