package officialhy2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/quic-go/http3"
)

// Invalid replacements must preserve a usable certificate for new TLS and H3
// handshakes, while a fresh process must reject those same invalid files.
func TestCertificateValidityReplacement(t *testing.T) {
	for _, kind := range []string{"expired", "not_yet_valid", "expired_chain", "future_chain"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			cf, kf := filepath.Join(dir, "cert"), filepath.Join(dir, "key")
			pool := x509.NewCertPool()
			now := time.Now()
			pair := func(serial int64, from, to time.Time) ([]byte, []byte) {
				t.Helper()
				key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				spec := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: from, NotAfter: to, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
				der, e := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
				if e != nil {
					t.Fatal(e)
				}
				priv, e := x509.MarshalPKCS8PrivateKey(key)
				if e != nil {
					t.Fatal(e)
				}
				cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
				pool.AppendCertsFromPEM(cert)
				return cert, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})
			}
			write := func(path string, b []byte) {
				t.Helper()
				if e := os.WriteFile(path+".next", b, 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Rename(path+".next", path); e != nil {
					t.Fatal(e)
				}
			}
			first, key := pair(1, now.Add(-time.Hour), now.Add(30*24*time.Hour))
			write(cf, first)
			write(kf, key)
			cache, e := newCertificateCache(cf, kf, 0)
			if e != nil {
				t.Fatal(e)
			}
			udp, e := net.ListenPacket("udp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer udp.Close()
			hs, e := server.NewServer(&server.Config{Conn: udp, TLSConfig: server.TLSConfig{GetCertificate: cache.GetCertificate}, Authenticator: &Authenticator{},
				MasqHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "healthy page") })})
			if e != nil {
				t.Fatal(e)
			}
			defer hs.Close()
			go hs.Serve()
			h3check := func(serial int64) {
				t.Helper()
				transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
				defer transport.Close()
				client := http.Client{Transport: transport, Timeout: 3 * time.Second}
				res, e := client.Get("https://" + udp.LocalAddr().String() + "/")
				if e != nil {
					t.Fatal(e)
				}
				defer res.Body.Close()
				b, e := io.ReadAll(res.Body)
				if e != nil || res.StatusCode != 200 || res.ProtoMajor != 3 || string(b) != "healthy page" || res.TLS.PeerCertificates[0].SerialNumber.Int64() != serial {
					t.Fatalf("H3 page/certificate mismatch: serial wanted=%d", serial)
				}
			}
			listener, e := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{GetCertificate: cache.GetCertificate})
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			verify := func() error {
				done := make(chan struct{})
				go func() {
					defer close(done)
					peer, e := listener.Accept()
					if e == nil {
						peer.SetDeadline(time.Now().Add(3 * time.Second))
						_ = peer.(*tls.Conn).Handshake()
						peer.Close()
					}
				}()
				cl, e := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp4", listener.Addr().String(), &tls.Config{RootCAs: pool})
				if cl != nil {
					cl.Close()
				}
				<-done
				return e
			}
			if e := verify(); e != nil {
				t.Fatal("baseline", e)
			}
			h3check(1)
			from, to := now.Add(-48*time.Hour), now.Add(-24*time.Hour)
			if kind == "not_yet_valid" || kind == "future_chain" {
				from, to = now.Add(24*time.Hour), now.Add(30*24*time.Hour)
			}
			replacement, key := pair(2, from, to)
			if strings.HasSuffix(kind, "_chain") {
				expired := replacement
				replacement, key = pair(4, now.Add(-time.Hour), now.Add(48*time.Hour))
				replacement = append(replacement, expired...)
			}
			write(cf, replacement)
			write(kf, key)
			if fresh, e := newCertificateCache(cf, kf, 0); e == nil || fresh != nil {
				t.Fatal("invalid cold-start certificate accepted")
			}
			if e := verify(); e != nil {
				t.Fatalf("GOOD_CERT_DISPLACED: previous certificate remained valid but new handshake fails: %v", e)
			}
			h3check(1)
			if cache.lastReloadWarn.IsZero() {
				t.Fatal("missing rejected replacement warning")
			}
			replacement, key = pair(3, now.Add(-time.Hour), now.Add(60*24*time.Hour))
			write(cf, replacement)
			write(kf, key)
			if e := verify(); e != nil {
				t.Fatal("valid replacement did not recover", e)
			}
			h3check(3)
			if !cache.lastReloadWarn.IsZero() {
				t.Fatal("reload health did not recover")
			}
		})
	}
}
