package panel

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OxO-51888/V2node-HY2/conf"
)

type lostAckTransport struct {
	base http.RoundTripper
	lost bool
}

func (tr *lostAckTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := tr.base.RoundTrip(r)
	if err == nil && r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/traffic") && !tr.lost {
		tr.lost = true
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, fmt.Errorf("injected lost acknowledgement after real panel commit")
	}
	return response, err
}

func TestHY2PHPInteroperability(t *testing.T) {
	origin := os.Getenv("HY2_CONTRACT_URL")
	if origin == "" {
		t.Skip("isolated PHP panel required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", origin+"/__fixture/reset", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("fixture reset", res.StatusCode)
	}
	c, err := New(&conf.NodeConfig{APIHost: origin, NodeID: 1, Key: "fixture-token", StateDirectory: t.TempDir(), Timeout: 3})
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.GetNodeInfo(ctx)
	if err != nil || info == nil {
		t.Fatal("config", err)
	}
	c.CommitNodeInfo(info)
	info, err = c.GetNodeInfo(ctx)
	if err != nil || info != nil {
		t.Fatal("config conditional request", err)
	}
	users, err := c.GetUsers(ctx)
	if err != nil || users == nil || len(users.Users) != 1 {
		t.Fatal("users", err)
	}
	c.CommitUsers(users)
	users, err = c.GetUsers(ctx)
	if err != nil || users != nil {
		t.Fatal("users conditional request", err)
	}
	online := map[int][]string{1: {"192.0.2.55"}}
	if err = c.ReportNodeOnlineUsers(ctx, &online); err != nil {
		t.Fatal(err)
	}
	alive, err := c.GetUserAlive(ctx)
	if err != nil || alive[1] != 1 {
		t.Fatal("alive", alive, err)
	}
	tr := &lostAckTransport{base: http.DefaultTransport}
	c.client.SetTransport(tr)
	upload, download := int64(400), int64(600)
	capture := func() ([]UserTraffic, error) {
		if upload+download == 0 {
			return nil, nil
		}
		return []UserTraffic{{UID: 1, UUID: "user-1", Upload: upload, Download: download}}, nil
	}
	commits, revokes := 0, 0
	commit := func(rows []UserTraffic) error {
		commits++
		upload -= rows[0].Upload
		download -= rows[0].Download
		return nil
	}
	revoke := func(ids []int) error {
		for _, id := range ids {
			if id == 1 {
				revokes++
			}
		}
		return nil
	}
	if c.FlushTraffic(ctx, capture, commit, revoke) == nil {
		t.Fatal("lost response accepted")
	}
	if upload != 400 || download != 600 || commits != 0 {
		t.Fatal("unconfirmed counters removed")
	}
	if err = c.FlushTraffic(ctx, capture, commit, revoke); err != nil {
		t.Fatal(err)
	}
	if upload+download != 0 || commits != 1 || revokes != 1 {
		t.Fatalf("counters=%d/%d commits=%d revokes=%d", upload, download, commits, revokes)
	}
	users, err = c.GetUsers(ctx)
	if err != nil || users == nil || len(users.Users) != 0 {
		t.Fatal("exhausted user remains", err)
	}
	online = map[int][]string{}
	if err = c.ReportNodeOnlineUsers(ctx, &online); err != nil {
		t.Fatal(err)
	}
	alive, err = c.GetUserAlive(ctx)
	if err != nil || len(alive) != 0 {
		t.Fatal("empty heartbeat", alive, err)
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", origin+"/__fixture/counters", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var counters map[string]int
	if err = json.UnmarshalRead(res.Body, &counters); err != nil {
		t.Fatal(err)
	}
	if counters["upload"] != 400 || counters["download"] != 600 || counters["stats_upload"] != 400 || counters["sequence"] != 1 {
		t.Fatal(counters)
	}
	t.Log("real Go/PHP HTTP: conditional snapshots, lost ACK retry, exactly-once billing, quota revocation and empty heartbeat passed")
}
