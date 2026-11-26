package officialhy2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServingCertificateHealth(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	pool := x509.NewCertPool()
	pair := func(serial int64, valid time.Duration) ([]byte, []byte) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		spec := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(valid), IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, e := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		k, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		c := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		if !pool.AppendCertsFromPEM(c) {
			t.Fatal("certificate parse")
		}
		return c, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k})
	}
	write := func(path string, b []byte) {
		t.Helper()
		if e := os.WriteFile(path+".new", b, 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Rename(path+".new", path); e != nil {
			t.Fatal(e)
		}
	}
	c1, k1 := pair(1, 48*time.Hour)
	c2, k2 := pair(2, 30*24*time.Hour)
	write(cf, c1)
	write(kf, k1)
	cache, e := newCertificateCache(cf, kf, 0)
	if e != nil {
		t.Fatal(e)
	}
	if cache.lastExpiryWarn.IsZero() {
		t.Fatal("missing serving certificate expiry warning")
	}
	l, e := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{GetCertificate: cache.GetCertificate})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	handshake := func(want int64) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			s, e := l.Accept()
			if e == nil {
				e = s.(*tls.Conn).Handshake()
				s.Close()
			}
			done <- e
		}()
		c, e := tls.Dial("tcp4", l.Addr().String(), &tls.Config{RootCAs: pool})
		if e != nil {
			t.Fatal(e)
		}
		serial := c.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
		c.Close()
		if e := <-done; e != nil || serial != want {
			t.Fatalf("handshake serial=%d want=%d: %v", serial, want, e)
		}
	}
	handshake(1)
	write(kf, k2)
	handshake(1)
	alert := cache.lastReloadWarn
	expiry := cache.lastExpiryWarn
	if alert.IsZero() {
		t.Fatal("reload failure not reported")
	}
	handshake(1)
	if alert != cache.lastReloadWarn || expiry != cache.lastExpiryWarn {
		t.Fatal("warnings repeated immediately")
	}
	write(cf, c2)
	handshake(2)
	if !cache.lastReloadWarn.IsZero() {
		t.Fatal("reload failure did not clear")
	}
}
