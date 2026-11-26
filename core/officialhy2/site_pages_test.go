package officialhy2

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/quic-go/http3"
)

func pageWrite(t *testing.T, dir, name, data string) {
	t.Helper()
	f := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f+".new", []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f+".new", f); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPageBoundaries(t *testing.T) {
	for _, p := range []string{"", ".draft.html", ".staging/app.js", "config.json", "cert.key", "assets/", "bad\\name.js", "../index.html", "/index.html"} {
		if publicPageFile(p) {
			t.Errorf("unexpected public path %q", p)
		}
	}
	root := t.TempDir()
	h := sitePageHandler{root: root, site: "ovo"}
	pages := filepath.Join(root, h.site)
	req := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	if w := req("GET", "/"); w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing site must not return a successful blank page")
	}
	pageWrite(t, pages, "index.html", "")
	if req("GET", "/index.html").Code != 503 {
		t.Fatal("empty homepage must not be cached as a success")
	}
	pageWrite(t, pages, "assets/app.js", "alert(1);")
	if w := req("HEAD", "/assets/app.js"); w.Code != 200 || w.Body.Len() != 0 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal("incorrect HEAD or JavaScript content type")
	}
	if req("GET", "/assets/").Code != 404 || req("POST", "/").Code != 405 {
		t.Fatal("unexpected directory listing or method acceptance")
	}
	pageWrite(t, root, "gm/index.html", "other site")
	if err := os.Symlink(filepath.Join(root, "gm", "index.html"), filepath.Join(pages, "other.html")); err == nil {
		if req("GET", "/other.html").Code != 404 {
			t.Fatal("sibling site readable through link")
		}
	}
}

func TestSitePagesHTTP3Runtime(t *testing.T) {
	root := t.TempDir()
	h := sitePageHandler{root: root, site: "pianyi"}
	dir := filepath.Join(root, h.site)
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	cert, leaf := fixture.TLS.Certificates[0], fixture.Certificate()
	fixture.Close()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	srv, err := server.NewServer(&server.Config{Conn: conn, TLSConfig: server.TLSConfig{Certificates: []tls.Certificate{cert}}, Authenticator: &Authenticator{}, MasqHandler: h})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	tr := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer tr.Close()
	cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	check := func(path string, status int, body string) {
		t.Helper()
		res, err := cl.Get("https://" + conn.LocalAddr().String() + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil || res.ProtoMajor != 3 || res.StatusCode != status || !strings.Contains(string(b), body) {
			t.Fatalf("%s: proto=%s status=%d body=%q err=%v", path, res.Proto, res.StatusCode, b, err)
		}
	}
	check("/", 503, "temporarily unavailable")
	pageWrite(t, dir, "index.html", "<h1>First</h1>")
	pageWrite(t, dir, "assets/app.css", "body{color:black}")
	check("/", 200, "First")
	check("/assets/app.css?v=1", 200, "color:black")
	check("/assets/absent.css", 404, "not found")
	pageWrite(t, dir, "index.html", "<h1>Fresh</h1>")
	check("/index.html", 200, "Fresh")
}
