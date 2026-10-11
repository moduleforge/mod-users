package config

import (
	"fmt"
	"strings"
)

// ValidateCookieBasePath checks a configured cookie base path (USERS_COOKIE_PATH).
// The empty string is valid and means "derive from the request". A non-empty
// value must be an absolute path of one or more segments, each made only of
// RFC 3986 unreserved characters (letters, digits, '-', '.', '_', '~'), with no
// empty segments (so no trailing or doubled slash), no "." or ".." segments,
// and no whitespace, control characters, ';', ',', '%', or other characters
// that could alter or inject Set-Cookie attributes. Values such as "/" are
// rejected because base + marker would otherwise yield a "//" path.
func ValidateCookieBasePath(p string) error {
	if p == "" {
		return nil
	}
	if p[0] != '/' {
		return fmt.Errorf("cookie base path must start with '/'")
	}
	if len(p) == 1 {
		return fmt.Errorf("cookie base path must name at least one segment (leave unset for the root mount)")
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "" {
			return fmt.Errorf("cookie base path must not have a trailing or empty segment")
		}
		if seg == "." || seg == ".." {
			return fmt.Errorf("cookie base path must not contain '.' or '..' segments")
		}
		for i := 0; i < len(seg); i++ {
			if !isCookiePathSegmentByte(seg[i]) {
				return fmt.Errorf("cookie base path contains a disallowed character at offset %d of a segment; only letters, digits, '-', '.', '_', '~' are permitted", i)
			}
		}
	}
	return nil
}

func isCookiePathSegmentByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '.', b == '_', b == '~':
		return true
	}
	return false
}
