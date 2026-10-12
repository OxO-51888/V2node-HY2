package officialhy2

import (
	"sync"
	"testing"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"

	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/limiter"
	"go.uber.org/zap"
)

func TestTrafficLoggerCachesKnownUserAndCountsTraffic(t *testing.T) {
	userLimit := &limiter.UserLimitInfo{}
	limiterInfo := &limiter.Limiter{UserLimitInfo: &sync.Map{}}
	limiterInfo.UserLimitInfo.Store("tag|uuid", userLimit)
	logger := &trafficLogger{
		tag:       "tag",
		tagPrefix: "tag|",
		limiter:   limiterInfo,
		logger:    zap.NewNop(),
		counter:   counter.NewTrafficCounter(),
	}

	if ok := logger.LogTraffic("uuid", 11, 13); !ok {
		t.Fatal("LogTraffic rejected normal user")
	}
	if ok := logger.LogTraffic("uuid", 17, 19); !ok {
		t.Fatal("LogTraffic rejected cached user")
	}

	if up := logger.counter.GetUpCount("uuid"); up != 28 {
		t.Fatalf("up traffic = %d, want 28", up)
	}
	if down := logger.counter.GetDownCount("uuid"); down != 32 {
		t.Fatalf("down traffic = %d, want 32", down)
	}
}

func TestTrafficLoggerRevocationCachedConcurrentAndReadd(t *testing.T) {
	limiter.Init()
	users := []panel.UserInfo{{Id: 1, Uuid: "uuid"}}
	l := limiter.AddLimiter("hysteria2", "revoke", users, nil)
	defer limiter.DeleteLimiter("revoke")
	h := &trafficLogger{tag: "revoke", tagPrefix: "revoke|", limiter: l, logger: zap.NewNop(), counter: counter.NewTrafficCounter()}
	if h.LogTraffic("unknown", 10, 10) {
		t.Fatal("unknown user allowed")
	}
	if !h.LogTraffic("uuid", 10, 10) {
		t.Fatal("known user denied")
	}
	old := h.getUser("uuid")
	l.UpdateUser("revoke", nil, users, nil)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				if h.LogTraffic("uuid", 1, 1) {
					t.Error("revoked user forwarded traffic")
					return
				}
			}
		}()
	}
	wg.Wait()
	if !old.limit.Revoked.Load() || h.counter.GetUpCount("uuid") != 10 {
		t.Fatal("revoked cached traffic was counted")
	}
	l.UpdateUser("revoke", users, nil, nil)
	h.ForgetUser("uuid")
	if !h.LogTraffic("uuid", 1, 1) {
		t.Fatal("re-added user denied")
	}
	if h.getUser("uuid").limit == old.limit {
		t.Fatal("re-added user reused revoked state")
	}
}

func TestTrafficLoggerRejectsCachedOverLimitUser(t *testing.T) {
	userLimit := &limiter.UserLimitInfo{}
	limiterInfo := &limiter.Limiter{UserLimitInfo: &sync.Map{}}
	limiterInfo.UserLimitInfo.Store("tag|uuid", userLimit)
	logger := &trafficLogger{
		tag:       "tag",
		tagPrefix: "tag|",
		limiter:   limiterInfo,
		logger:    zap.NewNop(),
		counter:   counter.NewTrafficCounter(),
	}

	if ok := logger.LogTraffic("uuid", 1, 1); !ok {
		t.Fatal("LogTraffic rejected user before limit")
	}
	userLimit.OverLimit.Store(true)
	if ok := logger.LogTraffic("uuid", 1, 1); ok {
		t.Fatal("LogTraffic accepted over-limit user")
	}
	if userLimit.OverLimit.Load() {
		t.Fatal("over-limit flag was not consumed")
	}
}
