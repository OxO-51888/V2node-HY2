package officialhy2

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/limiter"
	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/core/v2/server"
	"go.uber.org/zap"
)

func TestRevocationHY2RuntimeSixSites(t *testing.T) {
	limiter.Init()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"} {
		for _, proto := range []string{"tcp", "udp"} {
			t.Run(site+"/"+proto, func(t *testing.T) {
				var forwarded atomic.Int64
				var originAddr string
				if proto == "tcp" {
					origin, err := net.Listen("tcp4", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer origin.Close()
					originAddr = origin.Addr().String()
					go func() {
						for {
							c, err := origin.Accept()
							if err != nil {
								return
							}
							go func() {
								defer c.Close()
								b := make([]byte, 4096)
								for {
									n, e := c.Read(b)
									if n > 0 {
										forwarded.Add(int64(n))
										c.Write(b[:n])
									}
									if e != nil {
										return
									}
								}
							}()
						}
					}()
				} else {
					origin, err := net.ListenPacket("udp4", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer origin.Close()
					originAddr = origin.LocalAddr().String()
					go func() {
						b := make([]byte, 4096)
						for {
							n, addr, e := origin.ReadFrom(b)
							if e != nil {
								return
							}
							forwarded.Add(int64(n))
							origin.WriteTo(b[:n], addr)
						}
					}()
				}
				users := []panel.UserInfo{{Id: 1, Uuid: "runtime-user"}}
				l := limiter.AddLimiter("hysteria2", site, users, nil)
				defer limiter.DeleteLimiter(site)
				h := &trafficLogger{tag: site, tagPrefix: site + "|", limiter: l, logger: zap.NewNop(), counter: counter.NewTrafficCounter()}
				auth := &Authenticator{users: map[string]int{"runtime-user": 1}}
				conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				srv, err := server.NewServer(&server.Config{Conn: conn, TLSConfig: server.TLSConfig{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, Authenticator: auth, TrafficLogger: h})
				if err != nil {
					t.Fatal(err)
				}
				defer srv.Close()
				go srv.Serve()
				manager := &Manager{nodes: map[string]*Node{site: {auth: auth, traffic: h}}}
				cfg := &hyclient.Config{ServerAddr: conn.LocalAddr(), Auth: "runtime-user", TLSConfig: hyclient.TLSConfig{InsecureSkipVerify: true}}
				client, _, err := hyclient.NewClient(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				payload := []byte("before-revocation")
				var send func([]byte) error
				var receive func() ([]byte, error)
				if proto == "tcp" {
					c, err := client.TCP(originAddr)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					send = func(b []byte) error { c.SetDeadline(time.Now().Add(2 * time.Second)); _, e := c.Write(b); return e }
					receive = func() ([]byte, error) { b := make([]byte, len(payload)); _, e := io.ReadFull(c, b); return b, e }
				} else {
					c, err := client.UDP()
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					send = func(b []byte) error { return c.Send(b, originAddr) }
					receive = func() ([]byte, error) { b, _, e := c.Receive(); return b, e }
				}
				if err := send(payload); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					b, e := receive()
					if e == nil && !bytes.Equal(b, payload) {
						e = io.ErrUnexpectedEOF
					}
					done <- e
				}()
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("initial echo timeout")
				}
				before := forwarded.Load()
				// DelUsers must revoke established sessions before limiter synchronization follows.
				if err := manager.DelUsers(site, users); err != nil {
					t.Fatal(err)
				}
				_ = send([]byte("must-not-forward"))
				go func() { _, e := receive(); done <- e }()
				select {
				case e := <-done:
					if e == nil {
						t.Fatal("revoked connection received data")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("revoked connection was not closed")
				}
				if forwarded.Load() != before {
					t.Fatal("revoked payload reached origin")
				}
				bad, _, err := hyclient.NewClient(cfg)
				if err == nil {
					bad.Close()
					t.Fatal("revoked authentication accepted")
				}
				l.UpdateUser(site, nil, users, nil)
				l.UpdateUser(site, users, nil, nil)
				if _, err := manager.AddUsers(site, users); err != nil {
					t.Fatal(err)
				}
				good, _, err := hyclient.NewClient(cfg)
				if err != nil {
					t.Fatal("re-added authentication:", err)
				}
				good.Close()
				t.Log("real HY2 forwarding, established-session revocation, authentication rejection and re-add passed")
			})
		}
	}
}
