package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestShutdownRetainsTailAndReplaysOnce(t *testing.T) {
	for _, failSync := range []bool{false, true} {
		t.Run(fmt.Sprint(failSync), func(t *testing.T) {
			var billed int64
			seen := map[uint64]bool{}
			reject := true
			c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiPath+"state" {
					w.Write([]byte(`{"next":1}`))
					return
				}
				body, _ := io.ReadAll(r.Body)
				var b trafficBatch
				json.Unmarshal(body, &b)
				if !seen[b.Sequence] {
					for _, row := range b.Rows {
						billed += row.Upload
					}
					seen[b.Sequence] = true
				}
				if reject {
					w.WriteHeader(503)
					return
				}
				json.MarshalWrite(w, TrafficAck{Accepted: true, Sequence: b.Sequence, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{}})
			})
			defer closeServer()
			counter := int64(100)
			capture := func() ([]UserTraffic, error) { return []UserTraffic{{UID: 1, UUID: "u", Upload: counter}}, nil }
			if c.FlushTraffic(context.Background(), capture, func([]UserTraffic) error { return nil }, noRevoke) == nil {
				t.Fatal("missing lost ACK")
			}
			counter = 175
			if failSync {
				c.syncDirectory = func(string) error { return fmt.Errorf("injected sync") }
				if c.PersistTraffic(context.Background(), capture) == nil {
					t.Fatal("sync failure ignored")
				}
				c.syncDirectory = nil
			}
			if err := c.PersistTraffic(context.Background(), capture); err != nil {
				t.Fatal(err)
			}
			if err := c.PersistTraffic(context.Background(), capture); err != nil {
				t.Fatal(err)
			}
			if err := c.PersistTraffic(context.Background(), func() ([]UserTraffic, error) { return nil, nil }); err != nil {
				t.Fatal(err)
			}
			reject = false
			restarted := &Client{client: c.client, outboxPath: c.outboxPath}
			if err := restarted.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return nil, nil }, func([]UserTraffic) error { t.Error("old bytes subtracted from new runtime"); return nil }, noRevoke); err != nil {
				t.Fatal(err)
			}
			if billed != 175 || len(seen) != 2 {
				t.Fatalf("billed=%d batches=%d", billed, len(seen))
			}
		})
	}
}

func TestBoundedReportingRotatesFairly(t *testing.T) {
	seen := map[int]bool{}
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiPath+"state" {
			w.Write([]byte(`{"next":1}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var b trafficBatch
		json.Unmarshal(body, &b)
		for _, row := range b.Rows {
			seen[row.UID] = true
		}
		json.MarshalWrite(w, TrafficAck{Accepted: true, Sequence: b.Sequence, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{}})
	})
	defer closeServer()
	rows := make([]UserTraffic, 1500)
	for i := range rows {
		rows[i] = UserTraffic{UID: i + 1, Upload: 1}
	}
	for i := 0; i < 3; i++ {
		if err := c.FlushTrafficLimit(context.Background(), func() ([]UserTraffic, error) { return rows, nil }, func([]UserTraffic) error { return nil }, noRevoke, 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1500 {
		t.Fatalf("starved users: %d", len(seen))
	}
}
