package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrafficIsolationForwardProgress(t *testing.T) {
	for _, mode := range []string{"live", "recovered", "sync-mark", "sync-finish", "restart-mark", "restart-finish", "users-down", "commit-fail", "revoke-fail"} {
		t.Run(mode, func(t *testing.T) {
			requests, commits, revokes := 0, 0, 0
			failed := false
			sequence := uint64(11)
			billed := int64(0)
			c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiPath+"users" {
					if r.Header.Get("If-None-Match") != "" {
						t.Error("isolation must request a full eligibility snapshot")
					}
					if mode == "users-down" && !failed {
						failed = true
						w.WriteHeader(503)
						return
					}
					w.Write([]byte(`{"users":[]}`))
					return
				}
				requests++
				body, _ := io.ReadAll(r.Body)
				var b trafficBatch
				_ = json.Unmarshal(body, &b)
				if b.Sequence <= 11 {
					conflictReply(w, body, 11, strings.Repeat("a", 64))
					return
				}
				if b.Sequence != sequence+1 {
					t.Error("wrong new sequence", b.Sequence, sequence)
					w.WriteHeader(409)
					return
				}
				sequence = b.Sequence
				for _, row := range b.Rows {
					billed += row.Upload + row.Download
				}
				_ = json.MarshalWrite(w, TrafficAck{Accepted: true, Sequence: b.Sequence, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Revoked: []int{1}})
			})
			defer closeServer()
			rows := []trafficRow{{UID: 1, UUID: "old", Upload: 25}}
			if err := c.saveReportState(&reportState{Version: 1, Next: 10, Pending: &trafficBatch{Sequence: 10, Rows: rows}, Buffered: []trafficRow{{UID: 2, Upload: 40}}}); err != nil {
				t.Fatal(err)
			}
			counter := int64(32)
			if mode == "recovered" {
				c = &Client{client: c.client, outboxPath: c.outboxPath}
				counter = 7
			}
			if strings.Contains(mode, "mark") || strings.Contains(mode, "finish") {
				syncs := 0
				faultAt := 2
				if strings.Contains(mode, "finish") {
					faultAt = 3
				}
				c.syncDirectory = func(path string) error {
					syncs++
					if syncs == faultAt {
						return fmt.Errorf("sync fault")
					}
					return syncReportDirectory(path)
				}
			}
			capture := func() ([]UserTraffic, error) {
				if counter == 0 {
					return nil, nil
				}
				return []UserTraffic{{UID: 1, UUID: "old", Upload: counter}}, nil
			}
			commit := func(rows []UserTraffic) error {
				if mode == "commit-fail" && !failed {
					failed = true
					return fmt.Errorf("commit fault")
				}
				for _, r := range rows {
					counter -= r.Upload
				}
				commits++
				return nil
			}
			revoke := func(ids []int) error {
				if mode == "revoke-fail" && !failed {
					failed = true
					return fmt.Errorf("revoke fault")
				}
				revokes += len(ids)
				return nil
			}
			err := c.FlushTraffic(context.Background(), capture, commit, revoke)
			if mode != "live" && mode != "recovered" {
				if err == nil {
					t.Fatal("fault not exercised")
				}
				if strings.HasPrefix(mode, "restart") {
					c = &Client{client: c.client, outboxPath: c.outboxPath}
					counter = 7
				}
				err = c.FlushTraffic(context.Background(), capture, commit, revoke)
			}
			if err != nil {
				t.Fatal(err)
			}
			if counter != 0 || billed != 47 || len(c.outbox.Held) != 1 || c.outbox.Held[0].Batch.Sequence != 10 || c.outbox.Pending != nil || c.outbox.Resolution != nil || c.outbox.Next != 14 || revokes == 0 || requests != 3 {
				t.Fatalf("mode=%s counter=%d billed=%d requests=%d commits=%d revokes=%d state=%+v", mode, counter, billed, requests, commits, revokes, c.outbox)
			}
			if err = c.FlushTraffic(context.Background(), capture, commit, revoke); err != nil || billed != 47 {
				t.Fatal("duplicate billing", err, billed)
			}
		})
	}
}

func TestTrafficIsolationUnknownAckBounded(t *testing.T) {
	for _, badCursor := range []bool{false, true} {
		t.Run(fmt.Sprint(badCursor), func(t *testing.T) {
			requests := 0
			c, done := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case apiPath + "users":
					w.Write([]byte(`{"users":[]}`))
				case apiPath + "state":
					if badCursor {
						w.Write([]byte(`{"next":12}`))
					} else {
						w.Write([]byte(`{"sequence":11,"next":12,"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
					}
				default:
					requests++
					w.Write([]byte(`{"accepted":false}`))
				}
			})
			defer done()
			_ = c.saveReportState(&reportState{Version: 1, Next: 10, Pending: &trafficBatch{Sequence: 10, Rows: []trafficRow{{UID: 1, Upload: 25}}}})
			commits := 0
			for i := 0; i < 3; i++ {
				err := c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) { return nil, nil }, func([]UserTraffic) error { commits++; return nil }, noRevoke)
				if (i < 2 || badCursor) && err == nil {
					t.Fatal("premature discard")
				}
				if i == 2 && !badCursor && err != nil {
					t.Fatal(err)
				}
			}
			if requests != 3 || (!badCursor && (commits != 1 || len(c.outbox.Held) != 1)) || (badCursor && (commits != 0 || c.outbox.Pending == nil)) {
				t.Fatal("invalid recovery", requests, commits, c.outbox)
			}
		})
	}
}

func TestTrafficIsolationBounds(t *testing.T) {
	now := time.Now().Unix()
	held := []trafficHold{{At: now - trafficHoldTTL, Batch: &trafficBatch{Sequence: 1}}}
	for i := 0; i < 100; i++ {
		held = append(held, trafficHold{At: now, Batch: &trafficBatch{Sequence: uint64(i + 2)}})
	}
	got := trimTrafficHolds(held, now)
	if len(got) != 16 || got[0].Batch.Sequence != 86 {
		t.Fatal("unbounded audit", got)
	}
	if len(trimTrafficHolds(got, now+trafficHoldTTL)) != 0 {
		t.Fatal("expired audit retained")
	}
}

func TestTrafficIsolationAuditCannotFillOutbox(t *testing.T) {
	c, done := newTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network") })
	defer done()
	// Huge audit data is deliberately discarded, never the real pending batch.
	s := &reportState{Version: 1, Next: 10, Pending: &trafficBatch{Sequence: 10, Rows: []trafficRow{{UID: 1, Upload: 2}}}}
	s.Held = []trafficHold{{At: time.Now().Unix(), Batch: &trafficBatch{Rows: make([]trafficRow, 800000)}}}
	if err := c.saveReportState(s); err != nil {
		t.Fatal(err)
	}
	if len(c.outbox.Held) != 0 || c.outbox.Pending.Rows[0].Upload != 2 {
		t.Fatal("audit blocked accounting")
	}
	if err := c.loadReportState(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTrafficIsolationStress(t *testing.T) {
	if os.Getenv("V2NODE_STRESS") != "1" {
		t.Skip("explicit stress mode required")
	}
	batch := &trafficBatch{Sequence: 10, Rows: []trafficRow{{UID: 1, Upload: 25}}}
	body, _ := json.Marshal(batch)
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	for _, site := range []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"} {
		t.Run(site, func(t *testing.T) {
			t.Parallel()
			for round := 1; round <= 10; round++ {
				for i := 0; i < 1000000; i++ {
					conflict := &trafficConflict{Sequence: 11, Next: 12, Digest: strings.Repeat("a", 64), RequestSequence: 10, RequestDigest: digest}
					if !canIsolateTraffic(conflict, batch) {
						t.Fatal("ahead not isolated")
					}
					conflict.RequestDigest = "wrong"
					if canIsolateTraffic(conflict, batch) {
						t.Fatal("forged proof accepted")
					}
				}
				t.Logf("site=%s round=%d iterations=1000000 isolation and mismatch checked", site, round)
			}
		})
	}
}
