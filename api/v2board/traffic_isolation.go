package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"log"
	"time"
)

const maxTrafficHolds = 16
const trafficHoldTTL = int64(7 * 24 * 60 * 60)

type unconfirmedTraffic struct{ error }

func (e *unconfirmedTraffic) Unwrap() error { return e.error }

// Resolution is persisted before RAM counters are consumed. It is never sent
// to the panel, even after a crash between committing counters and advancing.
type trafficResolution struct {
	Next uint64 `json:"next"`
	At   int64  `json:"at"`
}
type trafficHold struct {
	At    int64         `json:"at"`
	Batch *trafficBatch `json:"batch"`
}

func validTrafficIsolation(s *reportState) bool {
	if s.Failures < 0 || s.Failures > 3 || len(s.Held) > maxTrafficHolds {
		return false
	}
	if s.Resolution != nil && (s.Pending == nil || s.Resolution.Next == 0 || s.Resolution.Next > 1<<63-1 || s.Resolution.At <= 0) {
		return false
	}
	for _, h := range s.Held {
		if h.At <= 0 || h.Batch == nil || len(h.Batch.Rows) == 0 || len(h.Batch.Rows) > maxTrafficRows {
			return false
		}
	}
	return true
}

func trimTrafficHolds(held []trafficHold, now int64) []trafficHold {
	var result []trafficHold
	for _, h := range held {
		if h.At > now-trafficHoldTTL {
			result = append(result, h)
		}
	}
	if len(result) > maxTrafficHolds {
		result = result[len(result)-maxTrafficHolds:]
	}
	return result
}

func canIsolateTraffic(conflict *trafficConflict, batch *trafficBatch) bool {
	if batch == nil || conflict.Sequence <= batch.Sequence || conflict.Next != conflict.Sequence+1 || conflict.Next > 1<<63-1 || conflict.Next == 0 || !validTrafficDigest(conflict.Digest) || conflict.RequestSequence != batch.Sequence {
		return false
	}
	body, err := json.Marshal(batch)
	return err == nil && conflict.RequestDigest == fmt.Sprintf("%x", sha256.Sum256(body))
}

func (c *Client) readTrafficCursor(ctx context.Context) (uint64, error) {
	body, _, _, err := c.request(ctx, "GET", "state", "", nil)
	if err != nil {
		return 0, err
	}
	var remote struct {
		Sequence *uint64 `json:"sequence"`
		Next     uint64  `json:"next"`
		Digest   string  `json:"digest"`
	}
	if json.Unmarshal(body, &remote) != nil || remote.Sequence == nil || remote.Next == 0 || remote.Next > 1<<63-1 || remote.Next != *remote.Sequence+1 || (*remote.Sequence > 0 && !validTrafficDigest(remote.Digest)) || (*remote.Sequence == 0 && remote.Digest != "") {
		return 0, fmt.Errorf("invalid isolation cursor")
	}
	return remote.Next, nil
}

func (c *Client) isolateTraffic(ctx context.Context, next uint64, commit func([]UserTraffic) error, revoke func([]int) error) error {
	state := *c.outbox
	state.Resolution = &trafficResolution{Next: next, At: time.Now().Unix()}
	if err := c.saveReportState(&state); err != nil {
		return err
	}
	return c.finishTrafficIsolation(ctx, commit, revoke)
}

func (c *Client) finishTrafficIsolation(ctx context.Context, commit func([]UserTraffic) error, revoke func([]int) error) error {
	batch, resolution := c.outbox.Pending, c.outbox.Resolution
	// A lost ACK can hide revocations. Refresh eligibility without committing the
	// users ETag; the normal worker must still reconcile the full snapshot.
	c.InvalidateUsers()
	snapshot, err := c.getUsers(ctx, "")
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("missing users while resolving traffic")
	}
	active := make(map[int]bool, len(snapshot.Users))
	for _, u := range snapshot.Users {
		active[u.Id] = true
	}
	var revoked []int
	for _, row := range batch.Rows {
		if !active[row.UID] {
			revoked = append(revoked, row.UID)
		}
	}
	if err = revoke(revoked); err != nil {
		return err
	}
	if !c.committed && !c.recovered {
		rows := make([]UserTraffic, 0, len(batch.Rows))
		for _, r := range batch.Rows {
			rows = append(rows, UserTraffic{UID: r.UID, UUID: r.UUID, Upload: r.Upload, Download: r.Download})
		}
		if err = commit(rows); err != nil {
			return err
		}
		c.committed = true
	}
	held := append(append([]trafficHold(nil), c.outbox.Held...), trafficHold{At: resolution.At, Batch: batch})
	state := &reportState{Version: 1, Next: resolution.Next, Buffered: c.outbox.Buffered, Held: trimTrafficHolds(held, time.Now().Unix())}
	if err = c.saveReportState(state); err != nil {
		return err
	}
	c.lastTrafficUID = batch.Rows[len(batch.Rows)-1].UID
	log.Printf("traffic isolated without resubmission: node=%d sequence=%d rows=%d next=%d", c.NodeId, batch.Sequence, len(batch.Rows), state.Next)
	return nil
}
