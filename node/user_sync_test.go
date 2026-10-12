package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
				if strings.HasSuffix(r.URL.Path, "/alivelist") {
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
