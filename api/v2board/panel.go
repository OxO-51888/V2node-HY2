package panel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/go-resty/resty/v2"
)

const contractVersion = "hy2-1"
const apiPath = "/api/v2/server/hy2/"
const maxResponseBytes = 16 << 20

type Client struct {
	client                               *resty.Client
	APIHost                              string
	NodeId                               int
	cacheMu                              sync.Mutex
	nodeEtag, userEtag, responseBodyHash string
	outboxMu                             sync.Mutex
	outboxPath                           string
	outbox                               *reportState
	unsynced                             *reportState
	syncDirectory                        func(string) error
	recovered, committed                 bool
	draining                             bool
	drainSaved                           bool
	drainBase                            []trafficRow
	lastTrafficUID                       int
}

func New(c *conf.NodeConfig) (*Client, error) {
	u, err := url.Parse(c.APIHost)
	if err != nil {
		return nil, fmt.Errorf("invalid panel ApiHost")
	}
	loopbackHTTP := u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback()
	if (!loopbackHTTP && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("panel ApiHost must be an HTTPS origin without credentials or query")
	}
	if c.NodeID < 1 || c.Key == "" {
		return nil, fmt.Errorf("panel NodeID and ApiKey are required")
	}
	client := resty.New().SetBaseURL(strings.TrimRight(c.APIHost, "/")).
		SetAuthToken(c.Key).SetHeader("X-Node-ID", strconv.Itoa(c.NodeID)).
		SetHeader("X-V2node-Contract", contractVersion).
		SetHeader("Accept", "application/json").
		SetHeader("Cache-Control", "no-cache, no-store").
		SetHeader("User-Agent", "V2node-HY2/"+contractVersion).
		SetRedirectPolicy(resty.NoRedirectPolicy()).SetRetryCount(0)
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = conf.DefaultNodeTimeout
	}
	client.SetTimeout(time.Duration(timeout) * time.Second)
	dir := c.StateDirectory
	if dir == "" {
		dir = "/var/lib/v2node-hy2"
	}
	identity := sha256.Sum256([]byte(strings.TrimRight(c.APIHost, "/") + ":" + strconv.Itoa(c.NodeID)))
	return &Client{client: client, APIHost: c.APIHost, NodeId: c.NodeID,
		outboxPath: filepath.Join(dir, fmt.Sprintf("%x.json", identity[:16]))}, nil
}

// Bounded reads reject HTML login pages and incompatible panels instead of accepting them as ACKs.
func (c *Client) request(ctx context.Context, method, action, etag string, body []byte) ([]byte, string, bool, error) {
	rq := c.client.R().SetContext(ctx).SetDoNotParseResponse(true)
	if etag != "" {
		rq.SetHeader("If-None-Match", etag)
	}
	if body != nil {
		rq.SetHeader("Content-Type", "application/json").SetBody(body)
	}
	r, err := rq.Execute(method, apiPath+action)
	if err != nil {
		return nil, "", false, fmt.Errorf("panel %s transport: %w", action, err)
	}
	if r == nil || r.RawResponse == nil || r.RawResponse.Body == nil {
		return nil, "", false, fmt.Errorf("panel %s: missing response", action)
	}
	defer r.RawResponse.Body.Close()
	conflict := r.StatusCode() == 409 && method == "POST" && action == "traffic"
	if r.StatusCode() != 200 && r.StatusCode() != 304 && !conflict {
		return nil, "", false, fmt.Errorf("panel %s: status %d", action, r.StatusCode())
	}
	if r.Header().Get("X-V2node-Contract") != contractVersion {
		return nil, "", false, fmt.Errorf("panel %s: incompatible contract; install matching HY2 panel first", action)
	}
	if r.StatusCode() == 304 {
		if method != "GET" || etag == "" {
			return nil, "", false, fmt.Errorf("panel %s: unexpected 304", action)
		}
		return nil, etag, true, nil
	}
	if !strings.HasPrefix(strings.ToLower(r.Header().Get("Content-Type")), "application/json") {
		return nil, "", false, fmt.Errorf("panel %s: expected JSON", action)
	}
	data, err := io.ReadAll(io.LimitReader(r.RawResponse.Body, maxResponseBytes+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("panel %s body: %w", action, err)
	}
	if len(data) > maxResponseBytes {
		return nil, "", false, fmt.Errorf("panel %s: response too large", action)
	}
	if conflict {
		return nil, "", false, decodeTrafficConflict(data)
	}
	return data, r.Header().Get("ETag"), false, nil
}
