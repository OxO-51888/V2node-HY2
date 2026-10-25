//go:build linux

package cmd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
)

func TestContractRuntimeChild(t *testing.T) {
	if os.Getenv("HY2_RUNTIME_CHILD") == "" {
		t.Skip("subprocess only")
	}
	config = os.Getenv("HY2_RUNTIME_CONFIG")
	watch = true
	serverHandle(nil, nil)
}

// Runs the real command, six controllers, QUIC listeners and echo traffic.
// Every endpoint and database fixture is private to this test.
func TestContractRuntimeProcess(t *testing.T) {
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"fixture.invalid"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := x509.MarshalPKCS8PrivateKey(key)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}), 0600)
	ports := make([]int, 6)
	for i := range ports {
		sock, e := net.ListenPacket("udp4", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		ports[i] = sock.LocalAddr().(*net.UDPAddr).Port
		sock.Close()
	}
	var slow, revoked atomic.Bool
	var heartbeats, blocked, configReads atomic.Int64
	var billingMu sync.Mutex
	sequences := map[int]uint64{}
	digests := map[int]string{}
	billed := map[int]int64{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-V2node-Contract", "hy2-1")
		w.Header().Set("Content-Type", "application/json")
		id, _ := strconv.Atoi(r.Header.Get("X-Node-ID"))
		if id < 1 || id > 6 {
			http.Error(w, "node", 400)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/api/v2/server/hy2/") {
		case "config":
			configReads.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"protocol": "hysteria2", "listen_ip": "127.0.0.1", "server_port": ports[id-1], "tls": 1, "up_mbps": 100, "down_mbps": 100,
				"tls_settings": map[string]any{"server_name": "fixture.invalid", "cert_mode": "file", "cert_file": certPath, "key_file": keyPath},
				"base_config":  map[string]any{"push_interval": 5, "pull_interval": 5}})
		case "users":
			users := []map[string]any{}
			if !(id == 1 && revoked.Load()) {
				users = append(users, map[string]any{"id": id, "uuid": fmt.Sprintf("user-%d", id), "speed_limit": 0, "device_limit": 0})
			}
			json.NewEncoder(w).Encode(map[string]any{"users": users})
		case "state":
			billingMu.Lock()
			next := sequences[id] + 1
			billingMu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"next": next})
		case "alive":
			if r.Method == "POST" {
				heartbeats.Add(1)
				io.Copy(io.Discard, r.Body)
				w.Write([]byte(`{"accepted":true}`))
			} else {
				w.Write([]byte(`{"alive":{}}`))
			}
		case "traffic":
			if slow.Load() {
				blocked.Add(1)
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Second):
				}
			}
			body, _ := io.ReadAll(r.Body)
			var b struct {
				Sequence uint64 `json:"sequence"`
				Rows     []struct {
					UID  int `json:"uid"`
					U, D int64
				}
			}
			if json.Unmarshal(body, &b) != nil {
				http.Error(w, "body", 422)
				return
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(body))
			billingMu.Lock()
			if b.Sequence == sequences[id]+1 {
				for _, row := range b.Rows {
					billed[id] += row.U + row.D
				}
				sequences[id] = b.Sequence
				digests[id] = digest
			} else if b.Sequence != sequences[id] || digests[id] != digest {
				billingMu.Unlock()
				http.Error(w, "sequence", 409)
				return
			}
			billingMu.Unlock()
			ids := []int{}
			if id == 1 && revoked.Load() {
				ids = append(ids, 1)
			}
			json.NewEncoder(w).Encode(map[string]any{"accepted": true, "sequence": b.Sequence, "digest": digest, "revoked": ids})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	origin, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		for {
			c, e := origin.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	nodes := []map[string]any{}
	for i := range ports {
		nodes = append(nodes, map[string]any{"ApiHost": api.URL, "NodeID": i + 1, "ApiKey": "fixture", "Timeout": 2, "RetryCount": 0, "StateDirectory": filepath.Join(dir, "state")})
	}
	cfg, _ := json.Marshal(map[string]any{"Log": map[string]any{"Level": "info"}, "Nodes": nodes})
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, cfg, 0600)
	var child *exec.Cmd
	var done chan error
	start := func() {
		child = exec.Command(os.Args[0], "-test.run=^TestContractRuntimeChild$", "-test.v")
		child.Env = append(os.Environ(), "HY2_RUNTIME_CHILD=1", "HY2_RUNTIME_CONFIG="+cfgPath)
		logFile, e := os.OpenFile(filepath.Join(dir, "child.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		child.Stdout = logFile
		child.Stderr = logFile
		if e = child.Start(); e != nil {
			t.Fatal(e)
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd, ch chan error) { ch <- cmd.Wait(); logFile.Close() }(child, done)
	}
	stop := func() {
		child.Process.Signal(syscall.SIGTERM)
		select {
		case e := <-done:
			if e != nil {
				log, _ := os.ReadFile(filepath.Join(dir, "child.log"))
				t.Fatalf("child: %v\n%s", e, log)
			}
		case <-time.After(10 * time.Second):
			child.Process.Kill()
			<-done
			t.Fatal("SIGTERM did not cancel/join tasks")
		}
		child = nil
	}
	defer func() {
		if child != nil {
			child.Process.Kill()
			<-done
		}
		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(dir, "child.log"))
			t.Log(string(log))
		}
	}()
	connect := func(id int) hyclient.Client {
		addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ports[id-1]}
		deadline := time.Now().Add(15 * time.Second)
		for {
			c, _, e := hyclient.NewClient(&hyclient.Config{ServerAddr: addr, Auth: fmt.Sprintf("user-%d", id), TLSConfig: hyclient.TLSConfig{InsecureSkipVerify: true}})
			if e == nil {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatalf("node %d not ready: %v", id, e)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	echo := func(c hyclient.Client) error {
		stream, e := c.TCP(origin.Addr().String())
		if e != nil {
			return e
		}
		defer stream.Close()
		stream.SetDeadline(time.Now().Add(3 * time.Second))
		payload := bytes.Repeat([]byte("runtime-payload"), 512)
		if _, e = stream.Write(payload); e != nil {
			return e
		}
		received := make([]byte, len(payload))
		_, e = io.ReadFull(stream, received)
		if e == nil && !bytes.Equal(payload, received) {
			e = fmt.Errorf("corrupt payload")
		}
		return e
	}
	start()
	clients := make([]hyclient.Client, 6)
	defer func() {
		for _, c := range clients {
			if c != nil {
				c.Close()
			}
		}
	}()
	for i := range clients {
		clients[i] = connect(i + 1)
		if e := echo(clients[i]); e != nil {
			t.Fatal(e)
		}
	}
	slow.Store(true)
	before := heartbeats.Load()
	until := time.Now().Add(8 * time.Second)
	for heartbeats.Load() < before+6 || blocked.Load() == 0 {
		if time.Now().After(until) {
			t.Fatal("slow traffic suppressed heartbeat")
		}
		if e := echo(clients[1]); e != nil {
			t.Fatal("forwarding during slow panel:", e)
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Log("six real HY2 nodes: forwarding and independent heartbeat continue during stalled traffic POST")
	stop()
	files, _ := filepath.Glob(filepath.Join(dir, "state", "*.json"))
	if len(files) != 6 {
		t.Fatalf("shutdown journals=%d", len(files))
	}
	tailRows := 0
	for _, file := range files {
		body, _ := os.ReadFile(file)
		var state struct {
			Buffered []any
			Pending  *struct{ Rows []any }
		}
		json.Unmarshal(body, &state)
		tailRows += len(state.Buffered)
		if state.Pending != nil {
			tailRows += len(state.Pending.Rows)
		}
	}
	if tailRows < 6 {
		t.Fatal("unconfirmed shutdown traffic lost")
	}
	slow.Store(false)
	start()
	for i := range clients {
		clients[i].Close()
		clients[i] = connect(i + 1)
		if e := echo(clients[i]); e != nil {
			t.Fatal(e)
		}
	}
	billingMu.Lock()
	chargedNodes := len(billed)
	billingMu.Unlock()
	if chargedNodes != 6 {
		t.Fatalf("replay missing nodes: %d", chargedNodes)
	}
	// An actual watched config write exercises cmd.reload, not a mock reload callback.
	beforeConfig := configReads.Load()
	os.WriteFile(cfgPath, append(cfg, '\n'), 0600)
	until = time.Now().Add(12 * time.Second)
	reloaded := false
	for time.Now().Before(until) {
		log, _ := os.ReadFile(filepath.Join(dir, "child.log"))
		if bytes.Contains(log, []byte("reload finished")) {
			reloaded = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !reloaded || configReads.Load() <= beforeConfig {
		t.Fatal("actual reload did not complete")
	}
	for i := range clients {
		clients[i].Close()
		clients[i] = connect(i + 1)
		if e := echo(clients[i]); e != nil {
			t.Fatal("post reload:", e)
		}
	}
	revoked.Store(true)
	until = time.Now().Add(8 * time.Second)
	denied := false
	for time.Now().Before(until) {
		if echo(clients[0]) != nil {
			denied = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !denied {
		t.Fatal("quota revocation did not stop established client")
	}
	if e := echo(clients[1]); e != nil {
		t.Fatal("revoking one user disrupted another node:", e)
	}
	stop()
	t.Log("durable tail replay, watched-config reload, quota revocation and SIGTERM drain passed")
}
