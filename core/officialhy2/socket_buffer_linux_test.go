package officialhy2

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/core/v2/server"
)

func TestUDPReceiveBufferDoesNotShrink(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before, err := udpReceiveBufferSize(conn)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := setUDPReceiveBuffer(conn, before/4)
	if err != nil || actual != before {
		t.Fatalf("buffer changed: before=%d after=%d error=%v", before, actual, err)
	}
}

func TestUDPReceiveBufferHY2Runtime(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("runtime-buffer-test-"), 8192)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body)
	}))
	defer origin.Close()
	udpOrigin, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udpOrigin.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := udpOrigin.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = udpOrigin.WriteToUDP(buf[:n], addr)
		}
	}()
	for _, site := range []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"} {
		t.Run(site, func(t *testing.T) {
			n := &Node{}
			conn, err := n.getConn(&panel.NodeInfo{Common: &panel.CommonNode{ListenIP: "127.0.0.1"}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			before, err := udpReceiveBufferSize(conn.(*net.UDPConn))
			if err != nil {
				t.Fatal(err)
			}
			srv, err := server.NewServer(&server.Config{
				Conn: conn, TLSConfig: server.TLSConfig{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}},
				Authenticator:   &Authenticator{users: map[string]int{"test-user": 1}},
				QUICConfig:      *n.getQUICConfig(),
				BandwidthConfig: *n.getBandwidthConfig(&panel.NodeInfo{Common: &panel.CommonNode{UpMbps: 8000, DownMbps: 8000}}),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			go srv.Serve()
			t.Run("clients", func(t *testing.T) {
				for i := 0; i < 8; i++ {
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						t.Parallel()
						c, _, err := hyclient.NewClient(&hyclient.Config{
							ServerAddr: conn.LocalAddr(), Auth: "test-user",
							TLSConfig: hyclient.TLSConfig{InsecureSkipVerify: true},
						})
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						stream, err := c.TCP(strings.TrimPrefix(origin.URL, "http://"))
						if err != nil {
							t.Fatal(err)
						}
						defer stream.Close()
						stream.SetDeadline(time.Now().Add(10 * time.Second))
						req, _ := http.NewRequest(http.MethodPost, origin.URL, bytes.NewReader(payload))
						req.Close = true
						if err := req.Write(stream); err != nil {
							t.Fatal(err)
						}
						response, err := http.ReadResponse(bufio.NewReader(stream), req)
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(response.Body)
						response.Body.Close()
						if err != nil || !bytes.Equal(body, payload) {
							t.Fatalf("TCP payload mismatch: bytes=%d error=%v", len(body), err)
						}
						udp, err := c.UDP()
						if err != nil {
							t.Fatal(err)
						}
						defer udp.Close()
						if err := udp.Send(payload[:1024], udpOrigin.LocalAddr().String()); err != nil {
							t.Fatal(err)
						}
						done := make(chan error, 1)
						go func() {
							b, _, err := udp.Receive()
							if err == nil && !bytes.Equal(b, payload[:1024]) {
								err = fmt.Errorf("UDP payload mismatch: bytes=%d", len(b))
							}
							done <- err
						}()
						select {
						case err := <-done:
							if err != nil {
								t.Fatal(err)
							}
						case <-time.After(10 * time.Second):
							t.Fatal("UDP proxy timed out")
						}
					})
				}
			})
			after, err := udpReceiveBufferSize(conn.(*net.UDPConn))
			if err != nil || after < before {
				t.Fatalf("QUIC reduced receive buffer: before=%d after=%d error=%v", before, after, err)
			}
			if !t.Failed() {
				t.Logf("%s: 8 HY2 clients passed TCP and UDP echo; receive buffer=%d", site, after)
			}
		})
	}
}

func TestUDPReceiveBufferClosedSocket(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := setUDPReceiveBuffer(conn, udpReceiveBufferRequest); err == nil {
		t.Fatal("closed socket accepted buffer configuration")
	}
}

func TestUDPReceiveBufferLiveListener(t *testing.T) {
	for _, obfs := range []string{"plain", "salamander"} {
		t.Run(obfs, func(t *testing.T) {
			n := &Node{}
			conn, err := n.getConn(&panel.NodeInfo{Common: &panel.CommonNode{
				ListenIP: "127.0.0.1", Obfs: obfs, ObfsPassword: "buffer-test-password",
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			peer, err := n.getConn(&panel.NodeInfo{Common: &panel.CommonNode{
				ListenIP: "127.0.0.1", Obfs: obfs, ObfsPassword: "buffer-test-password",
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			if udp, ok := conn.(*net.UDPConn); ok {
				size, err := udpReceiveBufferSize(udp)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("effective receive buffer: %d", size)
				if os.Getenv("V2NODE_REQUIRE_LARGE_UDP_BUFFER") == "1" && size < udpReceiveBufferRequest*2 {
					t.Fatalf("receive buffer %d below %d", size, udpReceiveBufferRequest*2)
				}
			}
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := []byte("udp-buffer-runtime-test")
			if _, err := peer.WriteTo(payload, conn.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 1500)
			nread, _, err := conn.ReadFrom(buf)
			if err != nil || !bytes.Equal(buf[:nread], payload) {
				t.Fatalf("UDP receive failed: bytes=%d error=%v", nread, err)
			}
		})
	}
}
