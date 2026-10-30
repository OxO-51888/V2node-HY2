//go:build linux

package cmd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
)

func TestContractRuntimePHP(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "normal"
		if conflict {
			name = "sequence-conflict"
		}
		t.Run(name, func(t *testing.T) { testContractRuntimePHP(t, conflict) })
	}
}

func testContractRuntimePHP(t *testing.T, conflict bool) {
	panelURL := os.Getenv("HY2_CONTRACT_URL")
	if panelURL == "" {
		t.Skip("isolated PHP/MySQL panel required")
	}
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, _ := x509.MarshalPKCS8PrivateKey(key)
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600)
	sock, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := sock.LocalAddr().(*net.UDPAddr)
	sock.Close()
	body, _ := json.Marshal(map[string]any{"port": addr.Port, "cert": certFile, "key": keyFile})
	httpClient := &http.Client{Timeout: 5 * time.Second}
	response, e := httpClient.Post(panelURL+"/__fixture/runtime", "application/json", bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("fixture", response.StatusCode)
	}
	cfg, _ := json.Marshal(map[string]any{"Log": map[string]any{"Level": "info"}, "Nodes": []map[string]any{{"ApiHost": panelURL, "NodeID": 1, "ApiKey": "fixture-token", "Timeout": 3, "RetryCount": 0, "StateDirectory": filepath.Join(dir, "state")}}})
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, cfg, 0600)
	child := exec.Command(os.Args[0], "-test.run=^TestContractRuntimeChild$", "-test.v")
	child.Env = append(os.Environ(), "HY2_RUNTIME_CHILD=1", "HY2_RUNTIME_CONFIG="+cfgPath)
	logFile, e := os.Create(filepath.Join(dir, "child.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer logFile.Close()
	child.Stdout = logFile
	child.Stderr = logFile
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	stopped := false
	defer func() {
		if !stopped {
			child.Process.Kill()
			<-done
		}
		if t.Failed() {
			b, _ := os.ReadFile(logFile.Name())
			t.Log(string(b))
		}
	}()
	var client hyclient.Client
	deadline := time.Now().Add(20 * time.Second)
	for {
		client, _, e = hyclient.NewClient(&hyclient.Config{ServerAddr: addr, Auth: "user-1", TLSConfig: hyclient.TLSConfig{InsecureSkipVerify: true}})
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HY2 start", e)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer client.Close()
	if conflict {
		seed := []byte(`{"sequence":1,"rows":[{"uid":1,"u":7,"d":11}]}`)
		req, _ := http.NewRequest("POST", panelURL+"/api/v2/server/hy2/traffic", bytes.NewReader(seed))
		req.Header.Set("Authorization", "Bearer fixture-token")
		req.Header.Set("X-Node-ID", "1")
		req.Header.Set("X-V2node-Contract", "hy2-1")
		req.Header.Set("Content-Type", "application/json")
		seedResponse, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		seedResponse.Body.Close()
		if seedResponse.StatusCode != 200 {
			t.Fatal("conflict seed", seedResponse.StatusCode)
		}
	}
	origin, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
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
	stream, e := client.TCP(origin.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	payload := bytes.Repeat([]byte("Q"), 32768)
	stream.SetDeadline(time.Now().Add(4 * time.Second))
	if _, e = stream.Write(payload); e != nil {
		t.Fatal(e)
	}
	received := make([]byte, len(payload))
	if _, e = io.ReadFull(stream, received); e != nil {
		t.Fatal(e)
	}
	stream.Close()
	if !bytes.Equal(payload, received) {
		t.Fatal("forwarded bytes corrupted")
	}
	var counters map[string]int64
	deadline = time.Now().Add(12 * time.Second)
	for {
		response, e = httpClient.Get(panelURL + "/__fixture/counters")
		if e != nil {
			t.Fatal(e)
		}
		e = json.NewDecoder(response.Body).Decode(&counters)
		response.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if counters["upload"]+counters["download"] >= int64(2*len(payload)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual forwarded traffic not billed", counters)
		}
		time.Sleep(100 * time.Millisecond)
	}
	wantUpload, wantDownload := int64(32768), int64(32768)
	if conflict {
		wantUpload += 7
		wantDownload += 11
	}
	if counters["upload"] != wantUpload || counters["download"] != wantDownload || counters["stats_upload"] != wantUpload {
		t.Fatal("real accounting mismatch", counters)
	}
	denied := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		next, e := client.TCP(origin.Addr().String())
		if e != nil {
			denied = true
			break
		}
		next.SetDeadline(time.Now().Add(300 * time.Millisecond))
		_, e = next.Write([]byte("denied"))
		var b [6]byte
		if e == nil {
			_, e = io.ReadFull(next, b[:])
		}
		next.Close()
		if e != nil {
			denied = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !denied {
		t.Fatal("real MySQL quota exhaustion did not revoke HY2 client")
	}
	child.Process.Signal(syscall.SIGTERM)
	select {
	case e = <-done:
		stopped = true
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("real panel shutdown timeout")
	}
	t.Logf("real HY2 process -> PHP/Laravel -> private MySQL: upload=%d download=%d stats=%d; quota revoked and SIGTERM drained", counters["upload"], counters["download"], counters["stats_upload"])
}
