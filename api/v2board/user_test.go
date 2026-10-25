package panel

import (
	"context"
	"net/http"
	"testing"
)

func TestUserSnapshotValidationAndCommit(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		status         int
		bad, unchanged bool
	}{
		{"empty", `{"users":[]}`, 200, false, false},
		{"user", `{"users":[{"id":1,"uuid":"u","speed_limit":5,"device_limit":2}]}`, 200, false, false},
		{"unchanged", "", 304, false, true},
		{"missing", `{}`, 200, true, false}, {"null", `{"users":null}`, 200, true, false},
		{"nested", `{"data":{"users":[]}}`, 200, true, false}, {"trailing", `{"users":[]}garbage`, 200, true, false},
		{"wrong-type", `{"users":{}}`, 200, true, false}, {"empty-response", "", 204, true, false},
		{"server-error", `{"users":[]}`, 503, true, false},
		{"bad-user", `{"users":[{"id":0,"uuid":""}]}`, 200, true, false},
		{"duplicate", `{"users":[{"id":1,"uuid":"a"},{"id":1,"uuid":"b"}]}`, 200, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"new"`)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			defer closeServer()
			c.userEtag = `"old"`
			snapshot, err := c.GetUsers(context.Background())
			if (err != nil) != tc.bad {
				t.Fatalf("snapshot=%v err=%v", snapshot, err)
			}
			if c.userEtag != `"old"` {
				t.Fatal("committed before core applied users")
			}
			if tc.bad || tc.unchanged {
				if snapshot != nil {
					t.Fatal("unexpected snapshot")
				}
				return
			}
			if snapshot == nil || snapshot.Users == nil {
				t.Fatal("empty list must be explicit")
			}
			c.CommitUsers(snapshot)
			if c.userEtag != `"new"` {
				t.Fatal("commit missing")
			}
		})
	}
}

func TestAliveFailureDoesNotEraseKnownState(t *testing.T) {
	for _, body := range []string{`{}`, `{"alive":null}`, `{"alive":{"1":-1}}`, `<html>login</html>`} {
		c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
		got, err := c.GetUserAlive(context.Background())
		closeServer()
		if err == nil || got != nil {
			t.Fatalf("invalid alive accepted: %s", body)
		}
	}
}
