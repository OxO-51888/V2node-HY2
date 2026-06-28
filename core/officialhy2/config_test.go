package officialhy2

import (
	"strings"
	"testing"

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
		"sg(suffix:chatgpt.com)",
		"sg(suffix:api.openai.com)",
		"sg(suffix:oaistatic.com)",
		"sg(suffix:oaiusercontent.com)",
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
