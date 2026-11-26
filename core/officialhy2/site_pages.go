package officialhy2

import (
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type sitePageHandler struct {
	root string
	site string
}

func publicPageFile(name string) bool {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".css", ".js", ".mjs", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".avif", ".ico", ".woff", ".woff2", ".ttf":
		return true
	}
	return false
}

func (h sitePageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if !publicPageFile(name) {
		http.NotFound(w, r)
		return
	}
	missing := func() {
		if name != "index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "Page temporarily unavailable", http.StatusServiceUnavailable)
	}
	if h.site == "" || !fs.ValidPath(h.site) || strings.ContainsAny(h.site, "/\\:") || strings.HasPrefix(h.site, ".") {
		missing()
		return
	}
	// Open on request so atomic page updates take effect without restarting HY2.
	base, err := os.OpenRoot(h.root)
	if err != nil {
		missing()
		return
	}
	defer base.Close()
	dirInfo, err := base.Lstat(h.site)
	if err != nil || !dirInfo.IsDir() {
		missing()
		return
	}
	dir, err := base.OpenRoot(h.site)
	if err != nil {
		missing()
		return
	}
	defer dir.Close()
	info, err := dir.Stat(name)
	if err != nil || !info.Mode().IsRegular() {
		missing()
		return
	}
	f, err := dir.Open(name)
	if err != nil {
		missing()
		return
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || (name == "index.html" && info.Size() == 0) {
		missing()
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	if typ := mime.TypeByExtension(path.Ext(name)); typ != "" {
		w.Header().Set("Content-Type", typ)
	}
	// Deployment can replace same-sized pages within one second. A zero
	// modtime avoids an incorrect date-based 304 for the previous version.
	http.ServeContent(w, r, name, time.Time{}, f)
}
