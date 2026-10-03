package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// The distroless runtime image has no /etc/mime.types, so the types the UI bundle needs are
// registered explicitly. Go's built-in table already knows some of them; AddExtensionType
// overrides it so behaviour is identical on every platform.
var spaMimeTypes = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".html":        "text/html; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".svg":         "image/svg+xml",
	".ico":         "image/x-icon",
	".png":         "image/png",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
}

func init() {
	for ext, t := range spaMimeTypes {
		_ = mime.AddExtensionType(ext, t)
	}
}

// spaMime returns the Content-Type for a file extension ("" when unknown).
func spaMime(ext string) string {
	if t, ok := spaMimeTypes[strings.ToLower(ext)]; ok {
		return t
	}
	return mime.TypeByExtension(ext)
}

const (
	cacheIndex     = "no-cache"
	cacheAsset     = "public, max-age=31536000, immutable"
	cacheRootFile  = "public, max-age=3600"
	uiNotBuiltPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Pabrika: UI not built</title></head>` +
		`<body><h1>UI not built</h1><p>This binary was built without the web bundle. ` +
		`Run <code>make build</code> (or <code>bun run build</code> in <code>web/</code> and rebuild the binary). ` +
		`The API under <code>/api/v1</code> is available.</p></body></html>`
)

// spaHandler serves the built Vue app: real files with cache headers, index.html for
// client-side routes, and a plain 404 for missing files. It never answers for the API-like
// prefixes (those get the JSON 404) and never uses http.FileServer (no redirects, no
// directory listings).
type spaHandler struct {
	fsys      fs.FS
	index     []byte // nil when the bundle is missing
	indexETag string

	etags sync.Map // file name -> ETag
}

// NewSPAHandler returns the handler for the static UI rooted at fsys (the contents of
// web/dist). When fsys has no index.html every UI path answers 503 "UI not built".
func NewSPAHandler(fsys fs.FS) http.Handler {
	h := &spaHandler{fsys: fsys}
	if b, err := fs.ReadFile(fsys, "index.html"); err == nil {
		h.index = b
		h.indexETag = etagOf(b)
	}
	return h
}

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// reservedPath reports whether p belongs to the API surface: those paths must never fall
// back to HTML.
func reservedPath(p string) bool {
	switch {
	case p == "/api", strings.HasPrefix(p, "/api/"):
	case p == "/mcp", strings.HasPrefix(p, "/mcp/"):
	case p == "/healthz", strings.HasPrefix(p, "/healthz/"):
	case strings.HasPrefix(p, "/.well-known/"):
	default:
		return false
	}
	return true
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := path.Clean("/" + r.URL.Path)
	if reservedPath(r.URL.Path) || reservedPath(clean) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "Not found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed")
		return
	}
	if h.index == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, uiNotBuiltPage)
		return
	}

	name := strings.TrimPrefix(clean, "/")
	if name == "" || name == "index.html" {
		h.serveIndex(w, r)
		return
	}
	if h.serveFile(w, r, name) {
		return
	}
	// Missing file: asset-like requests are real 404s (a stale hashed filename must not
	// return HTML), everything else is a client-side route.
	if name == "assets" || strings.HasPrefix(name, "assets/") || strings.Contains(path.Base(name), ".") {
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

func (h *spaHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", cacheIndex)
	hd.Set("ETag", h.indexETag)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.index))
}

// serveFile serves a regular file from the bundle and reports whether it existed.
func (h *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.IsDir() {
		return false
	}
	body, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	hd := w.Header()
	if t := spaMime(path.Ext(name)); t != "" {
		hd.Set("Content-Type", t)
	}
	if strings.HasPrefix(name, "assets/") {
		hd.Set("Cache-Control", cacheAsset)
	} else {
		hd.Set("Cache-Control", cacheRootFile)
	}
	et, ok := h.etags.Load(name)
	if !ok {
		et = etagOf(body)
		h.etags.Store(name, et)
	}
	hd.Set("ETag", et.(string))
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return true
}
