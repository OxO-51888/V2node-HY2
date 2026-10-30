package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func conflictReply(w http.ResponseWriter, body []byte, sequence uint64, digest string) {
	var batch trafficBatch
	_ = json.Unmarshal(body, &batch)
	w.WriteHeader(http.StatusConflict)
	_ = json.MarshalWrite(w, trafficConflict{Kind: "traffic_sequence_conflict", Sequence: sequence,
		Next: sequence + 1, Digest: digest, RequestSequence: batch.Sequence,
		RequestDigest: fmt.Sprintf("%x", sha256.Sum256(body))})
}

func TestTrafficSequenceConflictRecovery(t *testing.T) {
	for _, mode := range []string{"live", "lost-ack", "restart", "recovered", "fsync"} {
		t.Run(mode, func(t *testing.T) {
			sequence, digest := uint64(1), strings.Repeat("a", 64)
			billed, requests, commits := int64(100), 0, 0
			var c *Client
			outboxPath := filepath.Join(t.TempDir(), "traffic.json")
			var firstRows []byte
			c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiPath+"state" {
					w.Write([]byte(`{"next":1}`))
					return
				}
				requests++
				body, _ := io.ReadAll(r.Body)
				var batch trafficBatch
				if err := json.Unmarshal(body, &batch); err != nil {
					t.Error(err)
					return
				}
				rows, _ := json.Marshal(batch.Rows)
				if requests == 1 {
					firstRows = rows
				}
				if batch.Sequence <= 2 && !bytes.Equal(firstRows, rows) {
					t.Error("recovery changed traffic rows")
				}
				incoming := fmt.Sprintf("%x", sha256.Sum256(body))
				if batch.Sequence == sequence && digest != incoming {
					conflictReply(w, body, sequence, digest)
					return
				}
				if batch.Sequence == sequence+1 {
					sequence, digest = batch.Sequence, incoming
					for _, row := range batch.Rows {
						billed += row.Upload + row.Download
					}
				} else if batch.Sequence != sequence || digest != incoming {
					t.Error("invalid report accepted")
				}
				disk, _ := readReportState(outboxPath)
				var saved reportState
				_ = json.Unmarshal(disk, &saved)
				if saved.Pending == nil || saved.Pending.Sequence != batch.Sequence {
					t.Error("request sent before durable rebase")
				}
				if requests == 2 && (mode == "lost-ack" || mode == "restart") {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				_ = json.MarshalWrite(w, TrafficAck{Accepted: true, Sequence: sequence, Digest: digest, Revoked: []int{1}})
			})
			defer closeServer()
			c.outboxPath = outboxPath
			counter := int64(50)
			capture := func() ([]UserTraffic, error) {
				if counter == 0 {
					return nil, nil
				}
				return []UserTraffic{{UID: 1, UUID: "u1", Upload: counter}}, nil
			}
			commit := func(rows []UserTraffic) error { commits++; counter -= rows[0].Upload; return nil }
			revokes := 0
			revoke := func(ids []int) error { revokes += len(ids); return nil }
			if mode == "recovered" {
				if err := c.saveReportState(&reportState{Version: 1, Next: 1, Pending: &trafficBatch{Sequence: 1, Rows: []trafficRow{{UID: 1, Upload: 50}}}}); err != nil {
					t.Fatal(err)
				}
				c = &Client{client: c.client, outboxPath: c.outboxPath}
				counter = 25
			}
			if mode == "fsync" {
				calls := 0
				c.syncDirectory = func(path string) error {
					calls++
					if calls == 3 {
						return fmt.Errorf("injected rebase directory sync failure")
					}
					return syncReportDirectory(path)
				}
			}
			err := c.FlushTraffic(context.Background(), capture, commit, revoke)
			if mode == "live" || mode == "recovered" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || counter != 50 || commits != 0 {
					t.Fatal("fault not retained", err, counter, commits)
				}
				if mode == "restart" {
					c = &Client{client: c.client, outboxPath: c.outboxPath}
					counter = 25
				}
				if err = c.FlushTraffic(context.Background(), capture, commit, revoke); err != nil {
					t.Fatal(err)
				}
			}
			want := int64(150)
			if mode == "restart" || mode == "recovered" {
				want += 25
			}
			if billed != want || counter != 0 || commits != 1 || revokes < 1 || c.outbox.Pending != nil {
				t.Fatalf("billed=%d want=%d counter=%d commits=%d revokes=%d", billed, want, counter, commits, revokes)
			}
		})
	}
}

func TestTrafficConflictRejectsAmbiguousCursor(t *testing.T) {
	for _, scenario := range []string{"ahead", "behind", "same-digest", "uppercase-digest", "wrong-request", "wrong-next", "overflow", "bad-digest", "committed"} {
		t.Run(scenario, func(t *testing.T) {
			c, closeServer := newTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
			defer closeServer()
			state := &reportState{Version: 1, Next: 10, Pending: &trafficBatch{Sequence: 10, Rows: []trafficRow{{UID: 1, Upload: 25}}}}
			if err := c.saveReportState(state); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(state.Pending)
			digest := fmt.Sprintf("%x", sha256.Sum256(body))
			conflict := &trafficConflict{Kind: "traffic_sequence_conflict", Sequence: 10, Next: 11, Digest: strings.Repeat("a", 64), RequestSequence: 10, RequestDigest: digest}
			switch scenario {
			case "ahead":
				conflict.Sequence = 11
				conflict.Next = 12
			case "behind":
				conflict.Sequence = 9
				conflict.Next = 10
			case "same-digest":
				conflict.Digest = digest
			case "uppercase-digest":
				conflict.Digest = strings.ToUpper(digest)
			case "wrong-request":
				conflict.RequestDigest = strings.Repeat("b", 64)
			case "wrong-next":
				conflict.Next = 12
			case "overflow":
				conflict.Next = 0
			case "bad-digest":
				conflict.Digest = "bad"
			case "committed":
				c.committed = true
			}
			before, _ := os.ReadFile(c.outboxPath)
			if c.rebaseTrafficConflict(conflict) == nil {
				t.Fatal("unsafe rebase allowed")
			}
			after, _ := os.ReadFile(c.outboxPath)
			if !bytes.Equal(before, after) {
				t.Fatal("ambiguous traffic altered")
			}
		})
	}
}

func TestTrafficConflictRetriesAreBounded(t *testing.T) {
	requests := 0
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiPath+"state" {
			w.Write([]byte(`{"next":1}`))
			return
		}
		requests++
		body, _ := io.ReadAll(r.Body)
		var b trafficBatch
		_ = json.Unmarshal(body, &b)
		conflictReply(w, body, b.Sequence, strings.Repeat("a", 64))
	})
	defer closeServer()
	err := c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return []UserTraffic{{UID: 1, Upload: 1}}, nil }, func([]UserTraffic) error { t.Error("unconfirmed commit"); return nil }, noRevoke)
	if err == nil || requests != 2 || c.outbox.Pending == nil || c.outbox.Pending.Sequence != 2 {
		t.Fatal("unbounded or destructive conflict retry", requests, err)
	}
}

func TestTrafficConflictStress(t *testing.T) {
	if os.Getenv("V2NODE_STRESS") != "1" {
		t.Skip("explicit stress mode required")
	}
	digest := strings.Repeat("a", 64)
	for _, site := range []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"} {
		for round := 1; round <= 10; round++ {
			for i := uint64(1); i <= 1000000; i++ {
				conflict := trafficConflict{Sequence: i, Next: i + 1, Digest: strings.Repeat("b", 64), RequestSequence: i, RequestDigest: digest}
				if !canRebaseTraffic(&conflict, i, digest) {
					t.Fatal("valid conflict rejected")
				}
				conflict.Sequence++
				if canRebaseTraffic(&conflict, i, digest) {
					t.Fatal("ambiguous conflict accepted")
				}
			}
			t.Logf("site=%s round=%d iterations=1000000 valid_and_ambiguous_checked", site, round)
		}
	}
}
