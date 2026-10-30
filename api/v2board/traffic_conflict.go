package panel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
)

type trafficConflict struct {
	Kind            string `json:"error"`
	Sequence        uint64 `json:"sequence"`
	Digest          string `json:"digest"`
	Next            uint64 `json:"next"`
	RequestSequence uint64 `json:"request_sequence"`
	RequestDigest   string `json:"request_digest"`
}

func (e *trafficConflict) Error() string {
	return fmt.Sprintf("panel traffic sequence conflict: pending=%d remote=%d; unconfirmed traffic retained", e.RequestSequence, e.Sequence)
}

func decodeTrafficConflict(data []byte) error {
	var conflict trafficConflict
	if json.Unmarshal(data, &conflict) != nil || conflict.Kind != "traffic_sequence_conflict" {
		return fmt.Errorf("panel traffic: status 409 without a verifiable cursor; unconfirmed traffic retained")
	}
	return &conflict
}

func validTrafficDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == digest
}

func canRebaseTraffic(conflict *trafficConflict, sequence uint64, digest string) bool {
	return conflict.Sequence == sequence && conflict.RequestSequence == sequence &&
		conflict.RequestDigest == digest && validTrafficDigest(conflict.Digest) && conflict.Digest != digest &&
		conflict.Next == sequence+1 && conflict.Next != 0 && conflict.Next <= 1<<63-1
}

func (c *Client) rebaseTrafficConflict(conflict *trafficConflict) error {
	batch := c.outbox.Pending
	if batch == nil || c.committed {
		return conflict
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	// A different digest at this exact sequence proves this immutable batch was
	// not accepted there. A cursor further ahead cannot prove that: retain it.
	if !canRebaseTraffic(conflict, batch.Sequence, digest) {
		return conflict
	}
	// Persist the new identity before retrying. Do not alter counters, rows,
	// buffered shutdown traffic, or the recovered/current-runtime distinction.
	next := &reportState{Version: 1, Next: conflict.Next,
		Pending: &trafficBatch{Sequence: conflict.Next, Rows: batch.Rows}, Buffered: c.outbox.Buffered}
	return c.saveReportState(next)
}
