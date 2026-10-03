package service

import (
	"regexp"
	"strconv"
	"strings"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// IsULID reports whether s is a 26-character Crockford base32 string (case-insensitive).
func IsULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if strings.IndexByte(crockford, c) < 0 {
			return false
		}
	}
	// a ULID's first character is at most '7' (48-bit timestamp)
	return s[0] <= '7'
}

var (
	projectKeyRef = regexp.MustCompile(`^[A-Za-z]{2,6}$`)
	ticketRef     = regexp.MustCompile(`^([A-Za-z]{2,6})-([1-9][0-9]*)$`)
)

// ParseProjectRef classifies a project reference as a ULID (id, uppercased) or a key
// (key, uppercased). Anything else yields ErrNotFound so probing reveals nothing.
func ParseProjectRef(ref string) (id, key string, err error) {
	ref = strings.TrimSpace(ref)
	if IsULID(ref) {
		return strings.ToUpper(ref), "", nil
	}
	if projectKeyRef.MatchString(ref) {
		return "", strings.ToUpper(ref), nil
	}
	return "", "", errNotFound()
}

// ParseTicketRef classifies a ticket reference as a ULID (id) or a "KEY-N" reference
// (key uppercased, number). Anything else yields ErrNotFound.
func ParseTicketRef(ref string) (id, key string, number int64, err error) {
	ref = strings.TrimSpace(ref)
	if IsULID(ref) {
		return strings.ToUpper(ref), "", 0, nil
	}
	if m := ticketRef.FindStringSubmatch(ref); m != nil {
		n, perr := strconv.ParseInt(m[2], 10, 64)
		if perr == nil {
			return "", strings.ToUpper(m[1]), n, nil
		}
	}
	return "", "", 0, errNotFound()
}

// FormatRef renders a ticket reference such as "WEB-12".
func FormatRef(key string, number int64) string { return key + "-" + strconv.FormatInt(number, 10) }
