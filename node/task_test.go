package node

import (
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
)

func TestRequiresCoreReloadIgnoresRuntimeBaseConfig(t *testing.T) {
	oldNode := testNodeInfo()
	newNode := testNodeInfo()
	newNode.PushInterval = 15 * time.Second
	newNode.PullInterval = 20 * time.Second
	newNode.Common.BaseConfig.PushInterval = 15
	newNode.Common.BaseConfig.PullInterval = 20
	newNode.Common.BaseConfig.NodeReportMinTraffic = 1024
	newNode.Common.BaseConfig.DeviceOnlineMinTraffic = 2048

	if requiresCoreReload(oldNode, newNode) {
		t.Fatal("base config runtime changes should not require core reload")
	}
}

func TestRequiresCoreReloadDetectsInboundChange(t *testing.T) {
	oldNode := testNodeInfo()
	newNode := testNodeInfo()
	newNode.Common.ServerPort = 443

	if !requiresCoreReload(oldNode, newNode) {
		t.Fatal("server port change should require core reload")
	}
}

func TestRequiresCoreReloadDetectsTrustedForwarderChange(t *testing.T) {
	oldNode := testNodeInfo()
	newNode := testNodeInfo()
	newNode.Common.TrustedXForwardedFor = []string{"203.0.113.0/24"}

	if !requiresCoreReload(oldNode, newNode) {
		t.Fatal("trusted X-Forwarded-For change should require core reload")
	}
}

func testNodeInfo() *panel.NodeInfo {
	return &panel.NodeInfo{
		Id:           1,
		Type:         "hysteria2",
		Security:     panel.Tls,
		PushInterval: 10 * time.Second,
		PullInterval: 10 * time.Second,
		Tag:          "[https://example.com]-hysteria2:1",
		Common: &panel.CommonNode{
			Protocol:                "hysteria2",
			ListenIP:                "0.0.0.0",
			ServerPort:              8443,
			Tls:                     panel.Tls,
			TlsSettings:             panel.TlsSettings{ServerName: "example.com"},
			UpMbps:                  100,
			DownMbps:                100,
			Ignore_Client_Bandwidth: true,
			BaseConfig: &panel.BaseConfig{
				PushInterval:           10,
				PullInterval:           10,
				NodeReportMinTraffic:   0,
				DeviceOnlineMinTraffic: 0,
			},
		},
	}
}
