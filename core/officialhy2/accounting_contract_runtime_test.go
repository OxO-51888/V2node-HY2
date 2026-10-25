package officialhy2

import (
	"fmt"
	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/limiter"
	"os"
	"testing"
	"time"
)

func TestContractRuntimeRevocationBarrierAndTail(t *testing.T) {
	limiter.Init()
	users := []panel.UserInfo{{Id: 1, Uuid: "accounting-user"}}
	l := limiter.AddLimiter("hysteria2", "tail-test", users, nil)
	defer limiter.DeleteLimiter("tail-test")
	h := &trafficLogger{tag: "tail-test", tagPrefix: "tail-test|", limiter: l, counter: counter.NewTrafficCounter()}
	m := &Manager{nodes: map[string]*Node{"tail-test": {auth: &Authenticator{users: map[string]int{"accounting-user": 1}}, traffic: h}}}
	if !h.LogTraffic("accounting-user", 100, 200) {
		t.Fatal("initial traffic rejected")
	}
	cached := h.getUser("accounting-user")
	// Simulate a callback that passed its permission check before revocation.
	cached.trafficMu.RLock()
	done := make(chan error, 1)
	go func() { done <- m.DelUsers("tail-test", users) }()
	until := time.Now().Add(time.Second)
	for !cached.limit.Revoked.Load() {
		if time.Now().After(until) {
			t.Fatal("revocation not immediate")
		}
		time.Sleep(time.Millisecond)
	}
	_ = h.getUser("accounting-user")
	select {
	case <-done:
		t.Fatal("revocation did not join active accounting callback")
	default:
	}
	cached.storage.UpCounter.Add(50)
	cached.trafficMu.RUnlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if h.LogTraffic("accounting-user", 1, 1) {
		t.Fatal("revoked callback accepted")
	}
	rows := m.GetUserTrafficSlice("tail-test", 100000, func(string) int { return 1 })
	if len(rows) != 1 || rows[0].Upload != 150 || rows[0].Download != 200 {
		t.Fatalf("tail lost below reporting threshold: %+v", rows)
	}
	if len(m.PruneRetiredUsers("tail-test", []string{"accounting-user"})) != 0 {
		t.Fatal("unacknowledged tail pruned")
	}
	m.CommitUserTraffic("tail-test", rows)
	if len(m.PruneRetiredUsers("tail-test", []string{"accounting-user"})) != 1 {
		t.Fatal("acknowledged retired counter not reclaimed")
	}
}

func TestHY2AccountingStress(t *testing.T) {
	if os.Getenv("V2NODE_STRESS") != "1" {
		t.Skip("explicit stress mode required")
	}
	limiter.Init()
	for _, site := range []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"} {
		for round := 1; round <= 10; round++ {
			tag := fmt.Sprintf("accounting-%s-%d", site, round)
			users := []panel.UserInfo{{Id: 1, Uuid: "u"}}
			l := limiter.AddLimiter("hysteria2", tag, users, nil)
			h := &trafficLogger{tag: tag, tagPrefix: tag + "|", limiter: l, counter: counter.NewTrafficCounter()}
			m := &Manager{nodes: map[string]*Node{tag: {auth: &Authenticator{users: map[string]int{"u": 1}}, traffic: h}}}
			for i := 0; i < 1000000; i++ {
				if !h.LogTraffic("u", 1, 2) {
					t.Fatal("authorized traffic rejected")
				}
			}
			if err := m.DelUsers(tag, users); err != nil {
				t.Fatal(err)
			}
			rows := m.GetUserTrafficSlice(tag, 100000000, func(string) int { return 1 })
			if len(rows) != 1 || rows[0].Upload != 1000000 || rows[0].Download != 2000000 {
				t.Fatalf("counter mismatch: %+v", rows)
			}
			if h.LogTraffic("u", 1, 1) {
				t.Fatal("revoked traffic accepted")
			}
			m.CommitUserTraffic(tag, rows)
			if len(m.PruneRetiredUsers(tag, []string{"u"})) != 1 {
				t.Fatal("retired counter leak")
			}
			limiter.DeleteLimiter(tag)
			t.Logf("site=%s round=%d iterations=1000000 upload=1000000 download=2000000 retired=1", site, round)
		}
	}
}
