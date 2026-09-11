package officialhy2

import (
	"net"
	"strings"
	"testing"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/apernet/hysteria/extras/v2/outbounds"
)

func TestMasqSiteByPort(t *testing.T) {
	tests := map[int]string{
		51801: "gm",
		51802: "nnm",
		51803: "ovo",
		51804: "yiyuan",
		51805: "clash",
		51806: "pianyi",
		443:   "",
	}

	for port, want := range tests {
		if got := masqSiteByPort(port); got != want {
			t.Fatalf("masqSiteByPort(%d) = %q, want %q", port, got, want)
		}
	}
}

func TestACLRuleLine(t *testing.T) {
	tests := map[string]string{
		"netflix.com":            "sg(suffix:netflix.com)",
		".twitter.com":           "sg(suffix:twitter.com)",
		"suffix:disneyplus.com":  "sg(suffix:disneyplus.com)",
		"sg(suffix:example.com)": "sg(suffix:example.com)",
	}
	for input, want := range tests {
		if got := aclRuleLine("sg", input); got != want {
			t.Fatalf("aclRuleLine(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeListenIPSupportsDualStackWildcard(t *testing.T) {
	tests := map[string]string{
		"":              "",
		"*":             "",
		"0.0.0.0":       "",
		"::":            "",
		"[::]":          "",
		"2001:db8::1":   "2001:db8::1",
		"[2001:db8::1]": "2001:db8::1",
		"127.0.0.1":     "127.0.0.1",
	}
	for input, want := range tests {
		if got := normalizeListenIP(input); got != want {
			t.Fatalf("normalizeListenIP(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatAddressSupportsIPv6(t *testing.T) {
	tests := map[string]string{
		"":              ":8443",
		"127.0.0.1":     "127.0.0.1:8443",
		"2001:db8::1":   "[2001:db8::1]:8443",
		"[2001:db8::1]": "[2001:db8::1]:8443",
	}
	for input, want := range tests {
		if got := formatAddress(input, 8443); got != want {
			t.Fatalf("formatAddress(%q, 8443) = %q, want %q", input, got, want)
		}
	}
}

func TestQUICConfigUsesOfficialDefaults(t *testing.T) {
	n := &Node{}
	cfg := n.getQUICConfig()
	if cfg.InitialStreamReceiveWindow != defaultStreamReceiveWindow {
		t.Fatalf("initial stream receive window = %d, want %d", cfg.InitialStreamReceiveWindow, defaultStreamReceiveWindow)
	}
	if cfg.MaxStreamReceiveWindow != defaultStreamReceiveWindow {
		t.Fatalf("max stream receive window = %d, want %d", cfg.MaxStreamReceiveWindow, defaultStreamReceiveWindow)
	}
	if cfg.InitialConnectionReceiveWindow != defaultConnReceiveWindow {
		t.Fatalf("initial connection receive window = %d, want %d", cfg.InitialConnectionReceiveWindow, defaultConnReceiveWindow)
	}
	if cfg.MaxConnectionReceiveWindow != defaultConnReceiveWindow {
		t.Fatalf("max connection receive window = %d, want %d", cfg.MaxConnectionReceiveWindow, defaultConnReceiveWindow)
	}
	if cfg.MaxIncomingStreams != defaultMaxIncomingStreams {
		t.Fatalf("max incoming streams = %d, want %d", cfg.MaxIncomingStreams, defaultMaxIncomingStreams)
	}
}

func TestBandwidthConfigForcesServerBandwidth(t *testing.T) {
	n := &Node{}
	cfg := n.getBandwidthConfig(&panel.NodeInfo{
		Common: &panel.CommonNode{
			UpMbps:   500,
			DownMbps: 500,
		},
	})
	if !cfg.ForceServerBandwidth {
		t.Fatal("force server bandwidth is disabled")
	}
	if cfg.MaxTx != 500*megabyteSize/8 {
		t.Fatalf("max tx = %d, want %d", cfg.MaxTx, 500*megabyteSize/8)
	}
	if cfg.MaxRx != 500*megabyteSize/8 {
		t.Fatalf("max rx = %d, want %d", cfg.MaxRx, 500*megabyteSize/8)
	}
}

func TestUnlockRulesUseDefaultOutbound(t *testing.T) {
	n := &Node{unlock: &conf.UnlockConfig{
		Enable:          true,
		DefaultOutbound: "sg",
		SOCKS: []conf.SOCKSConfig{{
			Tag:     "sg",
			Address: "127.0.0.1",
			Port:    1080,
		}},
	}}
	rules := n.getUnlockRules("sg")
	for _, want := range []string{
		"sg(suffix:netflix.com)",
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("unlock rules missing %q in:\n%s", want, rules)
		}
	}
	for _, unwanted := range []string{
		"sg(suffix:x.com)",
		"sg(suffix:twitter.com)",
		"sg(suffix:t.co)",
		"sg(suffix:twimg.com)",
		"sg(suffix:chatgpt.com)",
		"sg(suffix:chat.openai.com)",
		"sg(suffix:openai.com)",
		"sg(suffix:api.openai.com)",
		"sg(suffix:auth.openai.com)",
		"sg(suffix:auth0.openai.com)",
		"sg(suffix:platform.openai.com)",
		"sg(suffix:oaistatic.com)",
		"sg(suffix:oaiusercontent.com)",
		"sg(suffix:cdn.openai.com)",
		"sg(suffix:gemini.google.com)",
		"sg(suffix:anthropic.com)",
		"sg(suffix:instagram.com)",
		"sg(suffix:cdninstagram.com)",
	} {
		if strings.Contains(rules, unwanted) {
			t.Fatalf("unlock rules should not include %q in:\n%s", unwanted, rules)
		}
	}
}

func TestUnlockOutboundsFallback(t *testing.T) {
	direct := outbounds.NewDirectOutboundSimple(outbounds.DirectOutboundModeAuto)
	n := &Node{unlock: &conf.UnlockConfig{
		Enable:          true,
		DefaultOutbound: "missing",
		SOCKS: []conf.SOCKSConfig{{
			Tag:     "sg",
			Address: "127.0.0.1",
			Port:    1080,
		}},
	}}
	tag, entries := n.getUnlockOutbounds(direct)
	if tag != "sg" {
		t.Fatalf("fallback unlock outbound = %q, want sg", tag)
	}
	if len(entries) != 3 {
		t.Fatalf("unlock entries = %d, want 3", len(entries))
	}
}

func TestUnlockOutboundConfigBuildsACL(t *testing.T) {
	n := &Node{unlock: &conf.UnlockConfig{
		Enable:          true,
		DefaultOutbound: "sg",
		SOCKS: []conf.SOCKSConfig{{
			Tag:     "sg",
			Address: "127.0.0.1",
			Port:    1080,
		}},
		Rules: []string{"netflix.com", "twitter.com"},
	}}
	if _, err := n.getOutboundConfig(); err != nil {
		t.Fatalf("getOutboundConfig() error = %v", err)
	}
}

func TestOutboundConfigUsesCoreDefaultWithoutUnlock(t *testing.T) {
	tests := []*conf.UnlockConfig{
		nil,
		{Enable: false},
		{Enable: true},
	}
	for _, unlock := range tests {
		n := &Node{unlock: unlock}
		outbound, err := n.getOutboundConfig()
		if err != nil {
			t.Fatalf("getOutboundConfig() error = %v", err)
		}
		if outbound != nil {
			t.Fatalf("outbound = %T, want nil core default when unlock is not configured", outbound)
		}
	}
}

func TestUnlockOutboundConfigUsesFastAdapter(t *testing.T) {
	n := &Node{unlock: &conf.UnlockConfig{
		Enable:          true,
		DefaultOutbound: "sg",
		SOCKS: []conf.SOCKSConfig{{
			Tag:     "sg",
			Address: "127.0.0.1",
			Port:    1080,
		}},
		Rules: []string{"netflix.com"},
	}}
	outbound, err := n.getOutboundConfig()
	if err != nil {
		t.Fatalf("getOutboundConfig() error = %v", err)
	}
	if _, ok := outbound.(*fastOutboundAdapter); !ok {
		t.Fatalf("outbound = %T, want *fastOutboundAdapter", outbound)
	}
}

func TestResolveAddrExTCPPrefersIPv4(t *testing.T) {
	got, err := resolveAddrExTCP(&outbounds.AddrEx{
		Host: "gemini.google.com",
		Port: 443,
		ResolveInfo: &outbounds.ResolveInfo{
			IPv4: net.ParseIP("142.251.1.1"),
			IPv6: net.ParseIP("2001:4860:4860::8888"),
		},
	})
	if err != nil {
		t.Fatalf("resolveAddrExTCP() error = %v", err)
	}
	if got != "142.251.1.1:443" {
		t.Fatalf("resolveAddrExTCP() = %q, want IPv4 endpoint", got)
	}
}

func TestParseAddrExSupportsIPv6(t *testing.T) {
	addr, err := parseAddrEx("[2001:db8::1]:443")
	if err != nil {
		t.Fatalf("parseAddrEx() error = %v", err)
	}
	if addr.Host != "2001:db8::1" || addr.Port != 443 {
		t.Fatalf("addr = %#v, want IPv6 host and port 443", addr)
	}
}

func TestMasqSiteByName(t *testing.T) {
	tests := map[string]string{
		"gm.example.com":      "gm",
		"nnm.example.com":     "nnm",
		"ovo.example.com":     "ovo",
		"yiyuan.example.com":  "yiyuan",
		"clash.example.com":   "clash",
		"pianyi.example.com":  "pianyi",
		"51801.example.com":   "gm",
		"51806.example.com":   "pianyi",
		"unknown.example.com": "",
		"clash.example.com.":  "clash",
		"  ovo.example.com  ": "ovo",
	}

	for name, want := range tests {
		if got := masqSiteByName(name); got != want {
			t.Fatalf("masqSiteByName(%q) = %q, want %q", name, got, want)
		}
	}
}
