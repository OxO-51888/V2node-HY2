package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"encoding/json"
)

// Security type
const (
	None = 0
	Tls  = 1
)

type NodeInfo struct {
	etag, bodyHash string
	Id             int
	Type           string
	Security       int
	PushInterval   time.Duration
	PullInterval   time.Duration
	Tag            string
	Common         *CommonNode
}

type CommonNode struct {
	Protocol                string      `json:"protocol"`
	ListenIP                string      `json:"listen_ip"`
	ServerPort              int         `json:"server_port"`
	Routes                  []Route     `json:"routes"`
	BaseConfig              *BaseConfig `json:"base_config"`
	Tls                     int         `json:"tls"`
	TlsSettings             TlsSettings `json:"tls_settings"`
	CertInfo                *CertInfo
	UpMbps                  int    `json:"up_mbps"`
	DownMbps                int    `json:"down_mbps"`
	Obfs                    string `json:"obfs"`
	ObfsPassword            string `json:"obfs_password"`
	Ignore_Client_Bandwidth bool   `json:"ignore_client_bandwidth"`
}

type Route struct {
	Id          int      `json:"id"`
	Match       []string `json:"match"`
	Action      string   `json:"action"`
	ActionValue *string  `json:"action_value"`
}

type BaseConfig struct {
	PushInterval           any `json:"push_interval"`
	PullInterval           any `json:"pull_interval"`
	DeviceOnlineMinTraffic int `json:"device_online_min_traffic"`
	NodeReportMinTraffic   int `json:"node_report_min_traffic"`
}

const (
	defaultPanelPushInterval = time.Minute
	defaultPanelPullInterval = time.Minute
	minPanelInterval         = 5 * time.Second
)

type TlsSettings struct {
	ServerName       string   `json:"server_name"`
	ServerNames      []string `json:"server_names"`
	CertMode         string   `json:"cert_mode"`
	CertFile         string   `json:"cert_file"`
	KeyFile          string   `json:"key_file"`
	TlsCert          string   `json:"tls_cert"`
	TlsKey           string   `json:"tls_key"`
	Provider         string   `json:"provider"`
	DNSEnv           string   `json:"dns_env"`
	RejectUnknownSni string   `json:"reject_unknown_sni"`
}

type CertInfo struct {
	CertMode         string
	CertFile         string
	KeyFile          string
	TlsCert          string
	TlsKey           string
	Email            string
	CertDomain       string
	DNSEnv           map[string]string
	Provider         string
	RejectUnknownSni bool
}

func (c *Client) GetNodeInfo(ctx context.Context) (node *NodeInfo, err error) {
	c.cacheMu.Lock()
	etag, appliedHash := c.nodeEtag, c.responseBodyHash
	c.cacheMu.Unlock()
	body, nextEtag, unchanged, err := c.request(ctx, "GET", "config", etag, nil)
	if err != nil || unchanged {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("empty node params response")
	}
	hash := sha256.Sum256(body)
	newBodyHash := hex.EncodeToString(hash[:])
	if appliedHash == newBodyHash {
		return nil, nil
	}
	node = &NodeInfo{
		Id: c.NodeId, etag: nextEtag, bodyHash: newBodyHash,
	}
	// parse protocol params
	cm := &CommonNode{}
	err = json.Unmarshal(body, cm)
	if err != nil {
		return nil, fmt.Errorf("decode node params error: %s", err)
	}
	switch cm.Protocol {
	case "hysteria2":
		node.Type = cm.Protocol
		node.Security = cm.Tls
	default:
		return nil, fmt.Errorf("unsupport protocol: %s", cm.Protocol)
	}
	if cm.ServerPort < 1 || cm.ServerPort > 65535 || cm.UpMbps < 0 || cm.DownMbps < 0 {
		return nil, fmt.Errorf("invalid HY2 port or bandwidth")
	}
	node.Tag = fmt.Sprintf("[%s]-%s:%d", c.APIHost, node.Type, node.Id)
	cf := cm.TlsSettings.CertFile
	kf := cm.TlsSettings.KeyFile
	if cf == "" {
		cf = filepath.Join("/etc/v2node/", cm.Protocol+strconv.Itoa(c.NodeId)+".cer")
	}
	if kf == "" {
		kf = filepath.Join("/etc/v2node/", cm.Protocol+strconv.Itoa(c.NodeId)+".key")
	}
	cm.CertInfo = &CertInfo{
		CertMode:         cm.TlsSettings.CertMode,
		CertFile:         cf,
		KeyFile:          kf,
		Email:            "node@v2board.com",
		CertDomain:       cm.TlsSettings.PrimaryServerName(),
		DNSEnv:           make(map[string]string),
		Provider:         cm.TlsSettings.Provider,
		RejectUnknownSni: cm.TlsSettings.RejectUnknownSni == "1",
	}
	if cm.CertInfo.CertMode == "dns" && cm.TlsSettings.DNSEnv != "" {
		envs := strings.Split(cm.TlsSettings.DNSEnv, ",")
		for _, env := range envs {
			kv := strings.SplitN(env, "=", 2)
			if len(kv) == 2 {
				cm.CertInfo.DNSEnv[kv[0]] = kv[1]
			}
		}
	}
	if cm.CertInfo.CertMode == "remote" {
		cm.CertInfo.TlsCert = cm.TlsSettings.TlsCert
		cm.CertInfo.TlsKey = cm.TlsSettings.TlsKey
	}

	if cm.BaseConfig == nil {
		cm.BaseConfig = &BaseConfig{
			PushInterval: defaultPanelPushInterval.Seconds(),
			PullInterval: defaultPanelPullInterval.Seconds(),
		}
	}

	// set interval
	node.PushInterval = intervalToTime(cm.BaseConfig.PushInterval, defaultPanelPushInterval)
	node.PullInterval = intervalToTime(cm.BaseConfig.PullInterval, defaultPanelPullInterval)

	node.Common = cm

	return node, nil
}

func (c *Client) CommitNodeInfo(node *NodeInfo) {
	if node == nil {
		return
	}
	c.cacheMu.Lock()
	c.nodeEtag, c.responseBodyHash = node.etag, node.bodyHash
	c.cacheMu.Unlock()
}

func intervalToTime(i interface{}, fallback time.Duration) time.Duration {
	var seconds int64
	switch v := i.(type) {
	case int:
		seconds = int64(v)
	case int8:
		seconds = int64(v)
	case int16:
		seconds = int64(v)
	case int32:
		seconds = int64(v)
	case int64:
		seconds = v
	case uint:
		seconds = int64(v)
	case uint8:
		seconds = int64(v)
	case uint16:
		seconds = int64(v)
	case uint32:
		seconds = int64(v)
	case uint64:
		if v > uint64(1<<63-1) {
			return fallback
		}
		seconds = int64(v)
	case float32:
		seconds = int64(v)
	case float64:
		seconds = int64(v)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return fallback
		}
		seconds = parsed
	default:
		return fallback
	}
	if seconds <= 0 {
		return fallback
	}
	interval := time.Duration(seconds) * time.Second
	if interval < minPanelInterval {
		return minPanelInterval
	}
	return interval
}

func (t TlsSettings) EffectiveServerNames() []string {
	if len(t.ServerNames) > 0 {
		return t.ServerNames
	}
	if t.ServerName == "" {
		return nil
	}
	return []string{t.ServerName}
}

func (t TlsSettings) PrimaryServerName() string {
	serverNames := t.EffectiveServerNames()
	if len(serverNames) == 0 {
		return ""
	}
	return serverNames[0]
}
