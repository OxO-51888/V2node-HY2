package officialhy2

import (
	"sync"
	"testing"

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
	userLimit.OverLimit = true
	if ok := logger.LogTraffic("uuid", 1, 1); ok {
		t.Fatal("LogTraffic accepted over-limit user")
	}
	if userLimit.OverLimit {
		t.Fatal("over-limit flag was not consumed")
	}
}
