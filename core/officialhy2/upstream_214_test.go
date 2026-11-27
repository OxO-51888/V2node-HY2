package officialhy2

import (
	"errors"
	"net"
	"testing"

	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/outbounds"
	"github.com/apernet/hysteria/extras/v2/outbounds/acl"
)

type routeMarker struct {
	err  error
	seen *outbounds.AddrEx
}

func (m *routeMarker) TCP(a *outbounds.AddrEx) (net.Conn, error) {
	m.seen = a
	return nil, m.err
}

func (m *routeMarker) UDP(a *outbounds.AddrEx) (outbounds.UDPConn, error) {
	m.seen = a
	return nil, m.err
}

func (m *routeMarker) CheckUDP(a *outbounds.AddrEx) error {
	m.seen = a
	return m.err
}

func TestUpstream214UnlockRouteParity(t *testing.T) {
	direct := &routeMarker{err: errors.New("direct")}
	unlock := &routeMarker{err: errors.New("unlock")}
	n := &Node{unlock: &conf.UnlockConfig{Enable: true}}
	rules, err := acl.TranslateText(n.getUnlockRules("unlock"), "unlock rules")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := outbounds.NewACLEngine(rules, []outbounds.OutboundEntry{
		{Name: "default", Outbound: direct},
		{Name: "direct", Outbound: direct},
		{Name: "unlock", Outbound: unlock},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fastOutboundAdapter{outbound: engine}
	info := &server.RequestInfo{AuthID: "test-user", ClientAddr: &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 40000}}
	for _, tc := range []struct {
		host string
		want *routeMarker
	}{
		{"netflix.com", unlock}, {"video.netflix.com", unlock},
		{"disneyplus.com", unlock}, {"netflix.com.example.org", direct},
		{"chatgpt.com", direct}, {"api.openai.com", direct},
		{"gemini.google.com", direct}, {"generativelanguage.googleapis.com", direct},
		{"x.com", direct}, {"video.twimg.com", direct}, {"instagram.com", direct},
		{"203.0.113.9", direct}, {"2001:db8::9", direct},
	} {
		t.Run(tc.host, func(t *testing.T) {
			addr := net.JoinHostPort(tc.host, "443")
			for _, method := range []string{"TCP", "UDP", "CheckUDP"} {
				t.Run(method, func(t *testing.T) {
					direct.seen, unlock.seen = nil, nil
					var got error
					switch method {
					case "TCP":
						_, got = adapter.TCP(info, addr)
					case "UDP":
						_, got = adapter.UDP(info, addr)
					default:
						got = adapter.CheckUDP(info, addr)
					}
					if !errors.Is(got, tc.want.err) {
						t.Fatalf("route = %v, want %v", got, tc.want.err)
					}
					if tc.want.seen == nil || tc.want.seen.Info != info || tc.want.seen.Port != 443 {
						t.Fatalf("request context lost: %+v", tc.want.seen)
					}
				})
			}
		})
	}
}

func TestUpstream214AdapterACLContextIsolation(t *testing.T) {
	selected := &routeMarker{err: errors.New("selected")}
	fallback := &routeMarker{err: errors.New("fallback")}
	engine, err := outbounds.NewACLEngine([]acl.Rule{
		{Outbound: "selected", Match: `auth.id == "alice" && client.ip == "192.0.2.1"`},
	}, []outbounds.OutboundEntry{{Name: "default", Outbound: fallback}, {Name: "selected", Outbound: selected}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fastOutboundAdapter{outbound: engine}
	for _, tc := range []struct {
		user, ip string
		want     *routeMarker
	}{
		{"alice", "192.0.2.1", selected}, {"bob", "192.0.2.1", fallback},
		{"alice", "192.0.2.2", fallback}, {"alice", "192.0.2.1", selected},
	} {
		info := &server.RequestInfo{AuthID: tc.user, ClientAddr: &net.UDPAddr{IP: net.ParseIP(tc.ip), Port: 40000}}
		_, tcpErr := adapter.TCP(info, "example.org:443")
		_, udpErr := adapter.UDP(info, "example.org:443")
		checkErr := adapter.CheckUDP(info, "example.org:443")
		for _, got := range []error{tcpErr, udpErr, checkErr} {
			if !errors.Is(got, tc.want.err) {
				t.Fatalf("user=%s ip=%s route=%v want=%v", tc.user, tc.ip, got, tc.want.err)
			}
		}
	}
}

func TestUpstream214InvalidUnlockRulesFailClosed(t *testing.T) {
	n := &Node{unlock: &conf.UnlockConfig{Enable: true,
		SOCKS: []conf.SOCKSConfig{{Tag: "unlock", Address: "127.0.0.1", Port: 1080}},
		Rules: []string{"missing(suffix:example.com)"},
	}}
	if ob, err := n.getOutboundConfig(); err == nil || ob != nil {
		t.Fatalf("invalid ACL accepted: outbound=%T error=%v", ob, err)
	}
}
