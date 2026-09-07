package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := resty.New().
		SetBaseURL(server.URL).
		SetQueryParams(map[string]string{
			"node_type": "v2node",
			"node_id":   "1",
			"token":     "test",
		})
	return &Client{
		client:  client,
		APIHost: server.URL,
		Token:   "test",
		NodeId:  1,
	}, server.Close
}

func TestGetNodeInfoRejectsInitialEmptyResponse(t *testing.T) {
	client, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})
	defer closeServer()

	_, err := client.GetNodeInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty node params response") {
		t.Fatalf("GetNodeInfo() error = %v, want empty response error", err)
	}
}

func TestGetNodeInfoSkipsEmptyCachedResponse(t *testing.T) {
	client, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})
	defer closeServer()
	client.responseBodyHash = "cached"

	info, err := client.GetNodeInfo(context.Background())
	if err != nil {
		t.Fatalf("GetNodeInfo() error = %v", err)
	}
	if info != nil {
		t.Fatalf("GetNodeInfo() = %#v, want nil cached skip", info)
	}
}

func TestGetNodeInfoRejectsServerError(t *testing.T) {
	client, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"error"}`))
	})
	defer closeServer()

	_, err := client.GetNodeInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get node info: status 500") {
		t.Fatalf("GetNodeInfo() error = %v, want status 500 error", err)
	}
}

func TestIntervalToTimeAppliesMinimum(t *testing.T) {
	if got := intervalToTime(1, time.Minute); got != minPanelInterval {
		t.Fatalf("intervalToTime(1) = %s, want %s", got, minPanelInterval)
	}
	if got := intervalToTime("15", time.Minute); got != 15*time.Second {
		t.Fatalf("intervalToTime(\"15\") = %s, want 15s", got)
	}
}
