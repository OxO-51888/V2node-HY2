package panel

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/OxO-51888/V2node-HY2/conf"
)

func TestHY2PHPConflictInteroperability(t *testing.T) {
	origin := os.Getenv("HY2_CONTRACT_URL")
	if origin == "" {
		t.Skip("isolated PHP/MySQL panel required")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Post(origin+"/__fixture/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("reset failed", res.StatusCode)
	}
	c, err := New(&conf.NodeConfig{APIHost: origin, NodeID: 1, Key: "fixture-token", StateDirectory: t.TempDir(), Timeout: 3})
	if err != nil {
		t.Fatal(err)
	}
	// The local cursor predates another accepted report, as in the incident.
	if err = c.saveReportState(&reportState{Version: 1, Next: 1}); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"sequence":1,"rows":[{"uid":1,"u":7,"d":11}]}`)
	req, _ := http.NewRequest("POST", origin+apiPath+"traffic", bytes.NewReader(old))
	req.Header.Set("Authorization", "Bearer fixture-token")
	req.Header.Set("X-Node-ID", "1")
	req.Header.Set("X-V2node-Contract", "hy2-1")
	req.Header.Set("Content-Type", "application/json")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("seed report", res.StatusCode)
	}
	u, d, commits := int64(400), int64(600), 0
	var revoked []int
	err = c.FlushTraffic(context.Background(), func() ([]UserTraffic, error) {
		if u+d == 0 {
			return nil, nil
		}
		return []UserTraffic{{UID: 1, UUID: "user-1", Upload: u, Download: d}}, nil
	}, func(rows []UserTraffic) error { commits++; u -= rows[0].Upload; d -= rows[0].Download; return nil }, func(ids []int) error { revoked = append(revoked, ids...); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if u+d != 0 || commits != 1 || len(revoked) != 1 || revoked[0] != 1 || c.outbox.Next != 3 {
		t.Fatal("recovery/limit failure", u, d, commits, revoked, c.outbox)
	}
	res, err = client.Get(origin + "/__fixture/counters")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var counters map[string]int64
	if err = json.UnmarshalRead(res.Body, &counters); err != nil {
		t.Fatal(err)
	}
	if counters["upload"] != 407 || counters["download"] != 611 || counters["stats_upload"] != 407 || counters["sequence"] != 2 {
		t.Fatal("double or missing billing", counters)
	}
	t.Log("real PHP/MySQL 409 -> durable rebase -> exact billing -> quota revocation passed")
}
