package panel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestGetUserListRejectsServerError(t *testing.T) {
	client, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"error"}`))
	})
	defer closeServer()

	_, err := client.GetUserList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get user list: status 500") {
		t.Fatalf("GetUserList() error = %v, want status 500 error", err)
	}
}

func TestUserListEmptyUnchangedAndInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		status             int
		wantError, wantNil bool
	}{
		{"empty", `{"users":[]}`, 200, false, false},
		{"unchanged", "", 304, false, true},
		{"missing", `{}`, 200, true, true},
		{"null", `{"users":null}`, 200, true, true},
		{"nested", `{"data":{"users":[]}}`, 200, true, true},
		{"truncated", `{"users":[]`, 200, true, true},
		{"trailing", `{"users":[]} garbage`, 200, true, true},
		{"wrong-type", `{"users":{}}`, 200, true, true},
		{"no-content", "", 204, true, true},
		{"error", `{"users":[]}`, 503, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", "new")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			defer closeServer()
			c.userEtag = "old"
			users, err := c.GetUserList(context.Background())
			if (err != nil) != tc.wantError || (users == nil) != tc.wantNil {
				t.Fatalf("users=%#v error=%v", users, err)
			}
			wantETag := "old"
			if !tc.wantError && tc.status == 200 {
				wantETag = "new"
			}
			if c.userEtag != wantETag {
				t.Fatalf("ETag=%q want %q", c.userEtag, wantETag)
			}
		})
	}
}

func TestUserListMessagePackEmpty(t *testing.T) {
	for _, users := range [][]UserInfo{nil, {}} {
		body, err := msgpack.Marshal(UserListBody{Users: users})
		if err != nil {
			t.Fatal(err)
		}
		c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-msgpack")
			_, _ = w.Write(body)
		})
		got, err := c.GetUserList(context.Background())
		closeServer()
		if users == nil {
			if err == nil {
				t.Fatal("null msgpack users accepted")
			}
		} else if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty msgpack users=%#v error=%v", got, err)
		}
	}
}
