package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

const maxTrafficRows = 500

func readReportState(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if len(body) > 16<<20 {
		return nil, fmt.Errorf("traffic state too large")
	}
	return body, err
}

type trafficRow struct {
	UID      int    `json:"uid"`
	Upload   int64  `json:"u"`
	Download int64  `json:"d"`
	UUID     string `json:"-"`
}
type trafficBatch struct {
	Sequence uint64       `json:"sequence"`
	Rows     []trafficRow `json:"rows"`
}
type TrafficAck struct {
	Accepted bool   `json:"accepted"`
	Sequence uint64 `json:"sequence"`
	Digest   string `json:"digest"`
	Revoked  []int  `json:"revoked"`
}
type reportState struct {
	Version  int           `json:"version"`
	Next     uint64        `json:"next"`
	Pending  *trafficBatch `json:"pending"`
	Buffered []trafficRow  `json:"buffered,omitempty"`
}

// One cursor and at most one pending batch per node: retries cannot grow a journal indefinitely.
func (c *Client) loadReportState(ctx context.Context) error {
	if c.unsynced != nil {
		disk, err := readReportState(c.outboxPath)
		if err != nil {
			return err
		}
		expected, _ := json.Marshal(c.unsynced)
		if !bytes.Equal(disk, expected) {
			return fmt.Errorf("unconfirmed traffic state changed on disk")
		}
		if err = c.finishStateSync(); err != nil {
			return err
		}
	}
	if c.outbox != nil {
		disk, err := readReportState(c.outboxPath)
		if err != nil {
			return err
		}
		current, _ := json.Marshal(c.outbox)
		if bytes.Equal(disk, current) {
			return nil
		}
		var saved reportState
		if c.committed && c.outbox.Pending != nil && json.Unmarshal(disk, &saved) == nil && saved.Version == 1 && saved.Pending == nil && saved.Next == c.outbox.Next+1 {
			c.outbox = &saved
			c.committed = false
			c.recovered = false
			return nil
		}
		return fmt.Errorf("traffic state changed by another controller; refusing to overwrite")
	}
	body, err := readReportState(c.outboxPath)
	if err == nil {
		var state reportState
		if json.Unmarshal(body, &state) != nil || state.Version != 1 || state.Next == 0 ||
			len(state.Buffered) > 200000 || (state.Pending != nil && (state.Pending.Sequence != state.Next || len(state.Pending.Rows) > maxTrafficRows || len(state.Pending.Rows) == 0)) {
			return fmt.Errorf("invalid traffic outbox; refusing to discard unconfirmed traffic")
		}
		c.outbox = &state
		c.recovered = state.Pending != nil
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read traffic outbox: %w", err)
	}
	body, _, _, err = c.request(ctx, "GET", "state", "", nil)
	if err != nil {
		return err
	}
	var remote struct {
		Next uint64 `json:"next"`
	}
	if json.Unmarshal(body, &remote) != nil || remote.Next == 0 {
		return fmt.Errorf("invalid report cursor")
	}
	state := &reportState{Version: 1, Next: remote.Next}
	if err = c.saveReportState(state); err != nil {
		return err
	}
	c.outbox = state
	return nil
}

func (c *Client) saveReportState(state *reportState) error {
	if c.outboxPath == "" {
		return fmt.Errorf("traffic state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(c.outboxPath), 0700); err != nil {
		return err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(body) > 16<<20 {
		return fmt.Errorf("traffic state exceeds durable size bound")
	}
	f, err := os.CreateTemp(filepath.Dir(c.outboxPath), ".traffic-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, c.outboxPath); err != nil {
		return err
	}
	c.unsynced = state
	return c.finishStateSync()
}

func (c *Client) finishStateSync() error {
	syncDir := c.syncDirectory
	if syncDir == nil {
		syncDir = syncReportDirectory
	}
	if err := syncDir(filepath.Dir(c.outboxPath)); err != nil {
		return err
	}
	c.outbox = c.unsynced
	c.unsynced = nil
	if c.outbox.Pending == nil {
		c.recovered, c.committed = false, false
	}
	return nil
}

func syncReportDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateTrafficAck(ack *TrafficAck, sequence uint64, digest string) error {
	if !ack.Accepted || ack.Sequence != sequence || ack.Digest != digest || ack.Revoked == nil {
		return fmt.Errorf("traffic acknowledgement mismatch")
	}
	for _, uid := range ack.Revoked {
		if uid <= 0 {
			return fmt.Errorf("invalid revoked user")
		}
	}
	return nil
}

func (c *Client) sendPending(ctx context.Context, commit func([]UserTraffic) error, revoke func([]int) error) error {
	for attempt := 0; ; attempt++ {
		err := c.sendPendingOnce(ctx, commit, revoke)
		var conflict *trafficConflict
		if attempt != 0 || !errors.As(err, &conflict) {
			return err
		}
		if err := c.rebaseTrafficConflict(conflict); err != nil {
			return err
		}
	}
}

func (c *Client) sendPendingOnce(ctx context.Context, commit func([]UserTraffic) error, revoke func([]int) error) error {
	batch := c.outbox.Pending
	if batch == nil {
		return nil
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	response, _, _, err := c.request(ctx, "POST", "traffic", "", body)
	if err != nil {
		return err
	}
	var ack TrafficAck
	if err = json.Unmarshal(response, &ack); err != nil {
		return fmt.Errorf("decode traffic acknowledgement: %w", err)
	}
	if err = validateTrafficAck(&ack, batch.Sequence, digest); err != nil {
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
	// Stop exhausted accounts before fetching another user snapshot or sending a new batch.
	if err = revoke(ack.Revoked); err != nil {
		return err
	}
	if len(batch.Rows) > 0 {
		c.lastTrafficUID = batch.Rows[len(batch.Rows)-1].UID
	}
	next := &reportState{Version: 1, Next: batch.Sequence + 1, Buffered: c.outbox.Buffered}
	if next.Next == 0 {
		return fmt.Errorf("report sequence overflow")
	}
	if err = c.saveReportState(next); err != nil {
		return err
	}
	c.outbox = next
	c.recovered = false
	c.committed = false
	return nil
}

// FlushTraffic replays the exact pending bytes before taking another counter snapshot.
func (c *Client) FlushTraffic(ctx context.Context, capture func() ([]UserTraffic, error), commit func([]UserTraffic) error, revoke func([]int) error) error {
	return c.FlushTrafficLimit(ctx, capture, commit, revoke, 0)
}

// A bounded round keeps large accounting backlogs from occupying control tasks.
func (c *Client) FlushTrafficLimit(ctx context.Context, capture func() ([]UserTraffic, error), commit func([]UserTraffic) error, revoke func([]int) error, batches int) error {
	c.outboxMu.Lock()
	defer c.outboxMu.Unlock()
	if c.draining {
		return fmt.Errorf("traffic outbox is draining")
	}
	if c.outboxPath == "" {
		return fmt.Errorf("traffic state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(c.outboxPath), 0700); err != nil {
		return err
	}
	guard := flock.New(c.outboxPath + ".lock")
	defer guard.Close()
	locked, err := guard.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("traffic state is already in use")
	}
	if err := c.loadReportState(ctx); err != nil {
		return err
	}
	hadPending := c.outbox.Pending != nil
	if err := c.sendPending(ctx, commit, revoke); err != nil {
		return err
	}
	if hadPending && batches > 0 {
		batches--
		if batches == 0 {
			return nil
		}
	}
	for len(c.outbox.Buffered) > 0 {
		selected, rest, err := selectTrafficBatch(c.outbox.Buffered)
		if err != nil {
			return err
		}
		next := &reportState{Version: 1, Next: c.outbox.Next, Pending: &trafficBatch{Sequence: c.outbox.Next, Rows: selected}, Buffered: rest}
		// Buffered bytes came from an earlier, quiesced runtime, not current counters.
		c.recovered = true
		if err := c.saveReportState(next); err != nil {
			return err
		}
		if err := c.sendPending(ctx, commit, revoke); err != nil {
			return err
		}
		if batches > 0 {
			batches--
			if batches == 0 {
				return nil
			}
		}
	}
	rows, err := capture()
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UID < rows[j].UID })
	pivot := sort.Search(len(rows), func(i int) bool { return rows[i].UID > c.lastTrafficUID })
	rows = append(append([]UserTraffic(nil), rows[pivot:]...), rows[:pivot]...)
	queued := make([]trafficRow, 0, len(rows))
	for _, r := range rows {
		queued = append(queued, trafficRow{UID: r.UID, UUID: r.UUID, Upload: r.Upload, Download: r.Download})
	}
	for len(queued) > 0 {
		if err = ctx.Err(); err != nil {
			return err
		}
		selected, rest, err := selectTrafficBatch(queued)
		if err != nil {
			return err
		}
		batch := &trafficBatch{Sequence: c.outbox.Next, Rows: selected}
		next := &reportState{Version: 1, Next: c.outbox.Next, Pending: batch}
		if err = c.saveReportState(next); err != nil {
			return err
		}
		c.outbox = next
		if err = c.sendPending(ctx, commit, revoke); err != nil {
			return err
		}
		queued = rest
		if batches > 0 {
			batches--
			if batches == 0 {
				return nil
			}
		}
	}
	return nil
}

// Rotated credentials can leave two counters for one UID. Never put duplicate
// UIDs in the same wire batch, and keep each UUID for its exact RAM commit.
func selectTrafficBatch(rows []trafficRow) ([]trafficRow, []trafficRow, error) {
	selected := make([]trafficRow, 0, min(len(rows), maxTrafficRows))
	var rest []trafficRow
	seen := map[int]bool{}
	for _, r := range rows {
		if r.UID <= 0 || r.Upload < 0 || r.Download < 0 {
			return nil, nil, fmt.Errorf("invalid traffic counters")
		}
		if len(selected) == maxTrafficRows || seen[r.UID] {
			rest = append(rest, r)
			continue
		}
		seen[r.UID] = true
		selected = append(selected, r)
	}
	return selected, rest, nil
}

// PersistTraffic is called only after forwarding and report workers stop.
// Pending bytes are already durable; only their uncommitted RAM overlap is removed.
func (c *Client) PersistTraffic(ctx context.Context, capture func() ([]UserTraffic, error)) error {
	c.outboxMu.Lock()
	defer c.outboxMu.Unlock()
	if c.outboxPath == "" {
		return fmt.Errorf("traffic state path is required")
	}
	guard := flock.New(c.outboxPath + ".lock")
	defer guard.Close()
	locked, err := guard.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("traffic state busy")
	}
	if err := c.loadReportState(ctx); err != nil {
		return err
	}
	if c.drainSaved {
		return nil
	}
	if !c.draining {
		c.drainBase = append([]trafficRow(nil), c.outbox.Buffered...)
		c.draining = true
	}
	rows, err := capture()
	if err != nil {
		return err
	}
	buffer := append([]trafficRow(nil), c.drainBase...)
	for _, r := range rows {
		if c.outbox.Pending != nil && !c.committed && !c.recovered {
			for _, pending := range c.outbox.Pending.Rows {
				if r.UUID == pending.UUID && r.UID == pending.UID {
					r.Upload -= pending.Upload
					r.Download -= pending.Download
				}
			}
		}
		if r.UID <= 0 || r.Upload < 0 || r.Download < 0 {
			return fmt.Errorf("inconsistent final counters")
		}
		if r.Upload+r.Download > 0 {
			buffer = append(buffer, trafficRow{UID: r.UID, Upload: r.Upload, Download: r.Download})
		}
	}
	if len(buffer) > 200000 {
		return fmt.Errorf("final accounting exceeds bounded outbox")
	}
	if err := c.saveReportState(&reportState{Version: 1, Next: c.outbox.Next, Pending: c.outbox.Pending, Buffered: buffer}); err != nil {
		return err
	}
	c.drainSaved = true
	return nil
}
