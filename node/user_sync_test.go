package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/OxO-51888/V2node-HY2/core"
	"github.com/OxO-51888/V2node-HY2/limiter"
)

func TestSyncUsersEmptyRevokesButFailurePreserves(t *testing.T) {
	limiter.Init()
	for _, tc := range []struct {
		name, body string
		status     int
		empty      bool
	}{
		{"empty", `{"users":[]}`, 200, true},
		{"unchanged", "", 304, false},
		{"error", `{"users":[]}`, 500, false},
		{"malformed", `{"users":[]`, 200, false},
		{"null", `{"users":null}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-V2node-Contract", "hy2-1")
				if !strings.HasPrefix(r.URL.Path, "/api/v2/server/hy2/") {
					t.Error("legacy API requested")
				}
				if strings.HasSuffix(r.URL.Path, "/alive") {
					w.Write([]byte(`{"alive":{}}`))
					return
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			retry := 0
			api, err := panel.New(&conf.NodeConfig{APIHost: srv.URL, NodeID: 1, Key: "test", Timeout: 2, RetryCount: &retry})
			if err != nil {
				t.Fatal(err)
			}
			if tc.status == 304 {
				api.CommitUsers(&panel.UserSnapshot{ETag: `"old"`})
			}
			users := []panel.UserInfo{{Id: 1, Uuid: "test-user"}}
			l := limiter.AddLimiter("hysteria2", tc.name, users, nil)
			defer limiter.DeleteLimiter(tc.name)
			backend := core.New(&conf.Conf{})
			defer backend.Close()
			c := &Controller{tag: tc.name, apiClient: api, limiter: l, server: backend, info: testNodeInfo(), userList: users}
			old, _ := l.UserLimitInfo.Load(tc.name + "|test-user")
			if err := c.syncUsers(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (len(c.userList) == 0) != tc.empty {
				t.Fatalf("unexpected user list: %#v", c.userList)
			}
			if old.(*limiter.UserLimitInfo).Revoked.Load() != tc.empty {
				t.Fatal("unexpected revocation")
			}
			if _, reject := l.CheckLimit(tc.name+"|test-user", "192.0.2.1", true); reject != tc.empty {
				t.Fatal("unexpected limiter state")
			}
		})
	}
}

func TestInFlightSnapshotCannotUndoQuotaRevocation(t *testing.T) {
	limiter.Init()
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-V2node-Contract", "hy2-1")
		w.Header().Set("Content-Type", "application/json")
		close(entered)
		<-release
		w.Write([]byte(`{"users":[{"id":1,"uuid":"test-user"}]}`))
	}))
	defer srv.Close()
	retry := 0
	api, err := panel.New(&conf.NodeConfig{APIHost: srv.URL, NodeID: 1, Key: "test", Timeout: 2, RetryCount: &retry})
	if err != nil {
		t.Fatal(err)
	}
	users := []panel.UserInfo{{Id: 1, Uuid: "test-user"}}
	l := limiter.AddLimiter("hysteria2", "stale", users, nil)
	defer limiter.DeleteLimiter("stale")
	backend := core.New(&conf.Conf{})
	defer backend.Close()
	c := &Controller{tag: "stale", apiClient: api, limiter: l, server: backend, info: testNodeInfo(), userList: users}
	done := make(chan error, 1)
	go func() { done <- c.syncUsers(context.Background()) }()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- c.revokeReportedUsers([]int{1}) }()
	select {
	case err := <-revoked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("slow GET blocked quota revocation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(c.userList) != 0 {
		t.Fatal("stale user snapshot restored exhausted user")
	}
}
