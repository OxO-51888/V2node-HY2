package panel

import (
	"context"
	"net/http"
	"strings"
	"testing"
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
