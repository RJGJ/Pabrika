package store

import (
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// fileDSN builds the DSN for an absolute file path. Write connections use _txlock=immediate
// so write transactions take the lock at BEGIN; read connections are query_only.
// journal_mode(WAL) is listed before query_only so it applies first.
func fileDSN(absPath string, writer bool) string {
	p := filepath.ToSlash(absPath)
	if !strings.HasPrefix(p, "/") { // Windows drive letter
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	for _, v := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)"} {
		q.Add("_pragma", v)
	}
	if writer {
		q.Set("_txlock", "immediate")
	} else {
		q.Add("_pragma", "query_only(1)")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func memoryDSN() string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	return "file::memory:?" + q.Encode()
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
