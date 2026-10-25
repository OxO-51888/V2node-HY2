package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
)

func noRevoke([]int) error { return nil }

func TestTrafficLostResponseAndNewTraffic(t *testing.T) {
	var savedBody []byte
	requests, billed, commits := 0, int64(0), 0
	first := true
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("credentials contract")
		}
		if r.URL.Path == apiPath+"state" {
			w.Write([]byte(`{"next":1}`))
			return
		}
		requests++
		body, _ := io.ReadAll(r.Body)
		var b trafficBatch
		json.Unmarshal(body, &b)
		if first {
			first = false
			savedBody = body
			billed += b.Rows[0].Upload
			hijacker := w.(http.Hijacker)
			conn, _, _ := hijacker.Hijack()
			conn.Close()
			return
		}
		if b.Sequence == 1 && string(body) != string(savedBody) {
			t.Fatal("retry mutated batch")
		}
		if b.Sequence == 2 {
			billed += b.Rows[0].Upload
		}
		ack, _ := json.Marshal(TrafficAck{Accepted: true, Sequence: b.Sequence, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{}})
		w.Write(ack)
	})
	defer closeServer()
	counter := int64(100)
	capture := func() ([]UserTraffic, error) {
		if counter == 0 {
			return nil, nil
		}
		return []UserTraffic{{UID: 1, UUID: "a", Upload: counter}}, nil
	}
	commit := func(rows []UserTraffic) error { commits++; counter -= rows[0].Upload; return nil }
	if c.FlushTraffic(context.Background(), capture, commit, noRevoke) == nil {
		t.Fatal("lost response accepted")
	}
	if counter != 100 || commits != 0 {
		t.Fatal("unconfirmed counters removed")
	}
	counter += 50
	if err := c.FlushTraffic(context.Background(), capture, commit, noRevoke); err != nil {
		t.Fatal(err)
	}
	if billed != 150 || counter != 0 || commits != 2 || requests != 3 {
		t.Fatalf("billed=%d counter=%d commits=%d requests=%d", billed, counter, commits, requests)
	}
}

func TestTrafficRestartReplaysWithoutSubtractingNewCounters(t *testing.T) {
	valid := false
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiPath+"state" {
			w.Write([]byte(`{"next":1}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var b trafficBatch
		json.Unmarshal(body, &b)
		if !valid {
			w.Write([]byte(`{"data":true}`))
			return
		}
		ack, _ := json.Marshal(TrafficAck{Accepted: true, Sequence: b.Sequence, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{1}})
		w.Write(ack)
	})
	defer closeServer()
	commits, revokes := 0, 0
	commit := func([]UserTraffic) error { commits++; return nil }
	if c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return []UserTraffic{{UID: 1, Upload: 100}}, nil }, commit, noRevoke) == nil {
		t.Fatal("legacy ack accepted")
	}
	valid = true
	restarted := &Client{client: c.client, outboxPath: c.outboxPath}
	if err := restarted.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return nil, nil }, commit, func(ids []int) error { revokes += len(ids); return nil }); err != nil {
		t.Fatal(err)
	}
	if commits != 0 || revokes != 1 || restarted.outbox.Next != 2 {
		t.Fatal("restart counter/revocation mismatch")
	}
}

func TestTrafficBadAcknowledgementsRetainBatch(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":true}`, `{"accepted":false}`, `{"accepted":true,"sequence":1,"digest":"wrong","revoked":[]}`, `<html>login</html>`} {
		c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == apiPath+"state" {
				w.Write([]byte(`{"next":1}`))
			} else {
				w.Write([]byte(body))
			}
		})
		commits := 0
		err := c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return []UserTraffic{{UID: 1, Upload: 20}}, nil }, func([]UserTraffic) error { commits++; return nil }, noRevoke)
		closeServer()
		if err == nil || commits != 0 || c.outbox.Pending == nil {
			t.Fatalf("bad ACK accepted: %s", body)
		}
	}
}

func TestTrafficCorruptStateFailsClosed(t *testing.T) {
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") })
	defer closeServer()
	os.WriteFile(c.outboxPath, []byte(`broken`), 0600)
	err := c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return nil, nil }, func([]UserTraffic) error { return nil }, noRevoke)
	if err == nil {
		t.Fatal("corrupt state discarded")
	}
}

func TestTrafficConcurrentFlushSerializes(t *testing.T) {
	var last uint64
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiPath+"state" {
			w.Write([]byte(`{"next":1}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var b trafficBatch
		json.Unmarshal(body, &b)
		if b.Sequence != last+1 {
			t.Errorf("out of order %d after %d", b.Sequence, last)
		}
		last = b.Sequence
		ack, _ := json.Marshal(TrafficAck{Accepted: true, Sequence: last, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{}})
		w.Write(ack)
	})
	defer closeServer()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return []UserTraffic{{UID: 1, Upload: 1}}, nil }, func([]UserTraffic) error { return nil }, noRevoke); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if last != 16 {
		t.Fatal(last)
	}
}

func TestHY2ContractStress(t *testing.T) {
	if os.Getenv("V2NODE_STRESS") != "1" {
		t.Skip("explicit stress mode required")
	}
	for _, site := range []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"} {
		for round := 1; round <= 10; round++ {
			var confirmed uint64
			for i := uint64(1); i <= 1000000; i++ {
				ack := TrafficAck{Accepted: true, Sequence: i, Digest: "digest", Revoked: []int{1}}
				if validateTrafficAck(&ack, i, "digest") != nil {
					t.Fatal("valid ack rejected")
				}
				if validateTrafficAck(&ack, i+1, "digest") == nil {
					t.Fatal("wrong sequence accepted")
				}
				if validateTrafficAck(&ack, i, "other") == nil {
					t.Fatal("wrong payload accepted")
				}
				confirmed++
			}
			t.Logf("site=%s round=%d iterations=%d confirmed=%d", site, round, 1000000, confirmed)
		}
	}
}

func TestTrafficDirectorySyncFailureRecovers(t *testing.T) {
	// The initial cursor, pending batch, and accepted cursor each have a rename/sync boundary.
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			billed, requests, commits := int64(0), 0, 0
			var sequence uint64
			c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiPath+"state" {
					w.Write([]byte(`{"next":1}`))
					return
				}
				requests++
				body, _ := io.ReadAll(r.Body)
				var batch trafficBatch
				json.Unmarshal(body, &batch)
				if batch.Sequence != sequence {
					billed += batch.Rows[0].Upload
					sequence = batch.Sequence
				}
				ack, _ := json.Marshal(TrafficAck{Accepted: true, Sequence: sequence, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{}})
				w.Write(ack)
			})
			defer closeServer()
			calls := 0
			c.syncDirectory = func(path string) error {
				calls++
				if calls == failAt {
					return fmt.Errorf("injected directory sync failure")
				}
				return syncReportDirectory(path)
			}
			counter := int64(100)
			capture := func() ([]UserTraffic, error) {
				if counter == 0 {
					return nil, nil
				}
				return []UserTraffic{{UID: 1, UUID: "a", Upload: counter}}, nil
			}
			commit := func(rows []UserTraffic) error {
				commits++
				if rows[0].UUID != "a" {
					t.Error("live counter identity lost")
				}
				counter -= rows[0].Upload
				return nil
			}
			if c.FlushTraffic(context.Background(), capture, commit, noRevoke) == nil {
				t.Fatal("fault not injected")
			}
			if err := c.FlushTraffic(context.Background(), capture, commit, noRevoke); err != nil {
				t.Fatal(err)
			}
			if counter != 0 || billed != 100 || commits != 1 || requests != 1 {
				t.Fatalf("counter=%d billed=%d commits=%d requests=%d", counter, billed, commits, requests)
			}
		})
	}
}
