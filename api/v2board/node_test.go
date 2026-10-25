package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPath) {
			t.Errorf("legacy request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-V2node-Contract", contractVersion)
		handler(w, r)
	}))
	client := resty.New().
		SetBaseURL(server.URL).
		SetAuthToken("test").SetHeader("X-Node-ID", "1").SetHeader("X-V2node-Contract", contractVersion)
	return &Client{
		client:     client,
		APIHost:    server.URL,
		NodeId:     1,
		outboxPath: filepath.Join(t.TempDir(), "traffic.json"),
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

func TestGetNodeInfoRejectsEmptyCachedResponse(t *testing.T) {
	client, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})
	defer closeServer()
	client.responseBodyHash = "cached"

	info, err := client.GetNodeInfo(context.Background())
	if err == nil || info != nil {
		t.Fatal("empty response silently accepted")
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
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("GetNodeInfo() error = %v, want status 500 error", err)
	}
}

func TestNodeCacheOnlyCommittedAfterApply(t *testing.T) {
	c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `W/"node1"`)
		w.Write([]byte(`{"protocol":"hysteria2","server_port":443,"tls":1}`))
	})
	defer closeServer()
	first, err := c.GetNodeInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.GetNodeInfo(context.Background())
	if err != nil || second == nil {
		t.Fatal("unapplied config was hidden")
	}
	c.CommitNodeInfo(first)
	third, err := c.GetNodeInfo(context.Background())
	if err != nil || third != nil {
		t.Fatal("applied hash not cached")
	}
}

func TestNodeValidationDoesNotPoisonCache(t *testing.T) {
	for _, body := range []string{`{"protocol":"vless","server_port":443}`, `{"protocol":"hysteria2","server_port":0}`, `{"status":"fail"}`, `null`} {
		c, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("ETag", "bad"); w.Write([]byte(body)) })
		_, err := c.GetNodeInfo(context.Background())
		closeServer()
		if err == nil || c.nodeEtag != "" || c.responseBodyHash != "" {
			t.Fatalf("bad config accepted: %s", body)
		}
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
