package officialhy2

import (
	"sync"

	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/limiter"
	"github.com/apernet/hysteria/core/v2/server"
	"go.uber.org/zap"
)

type trafficLogger struct {
	tag       string
	tagPrefix string
	limiter   *limiter.Limiter
	logger    *zap.Logger
	counter   *counter.TrafficCounter
	users     sync.Map
}

type trafficUserCache struct {
	storage *counter.TrafficStorage
	limit   *limiter.UserLimitInfo
}

func (h *trafficLogger) LogTraffic(id string, tx, rx uint64) bool {
	user := h.getUser(id)
	if user == nil {
		return false
	}
	if user.limit != nil && user.limit.OverLimit {
		user.limit.OverLimit = false
		return false
	}
	if rx > 0 {
		user.storage.DownCounter.Add(int64(rx))
	}
	if tx > 0 {
		user.storage.UpCounter.Add(int64(tx))
	}
	return true
}

func (h *trafficLogger) LogOnlineState(_ string, _ bool) {}

func (h *trafficLogger) TraceStream(_ server.HyStream, _ *server.StreamStats) {}

func (h *trafficLogger) UntraceStream(_ server.HyStream) {}

func (h *trafficLogger) TrackStreamStats() bool { return false }

func (h *trafficLogger) ForgetUser(id string) {
	h.users.Delete(id)
}

func (h *trafficLogger) getUser(id string) *trafficUserCache {
	if cached, ok := h.users.Load(id); ok {
		return cached.(*trafficUserCache)
	}
	limiterInfo, err := h.getLimiter()
	if err != nil {
		h.logger.Error("get limiter error", zap.String("tag", h.tag), zap.Error(err))
		return nil
	}

	var userLimitInfo *limiter.UserLimitInfo
	if userLimit, ok := limiterInfo.UserLimitInfo.Load(h.userTag(id)); ok {
		userLimitInfo = userLimit.(*limiter.UserLimitInfo)
	}
	user := &trafficUserCache{
		storage: h.counter.GetCounter(id),
		limit:   userLimitInfo,
	}
	if userLimitInfo == nil {
		return user
	}
	actual, _ := h.users.LoadOrStore(id, user)
	return actual.(*trafficUserCache)
}

func (h *trafficLogger) getLimiter() (*limiter.Limiter, error) {
	if h.limiter != nil {
		return h.limiter, nil
	}
	return limiter.GetLimiter(h.tag)
}

func (h *trafficLogger) userTag(uuid string) string {
	if h.tagPrefix != "" {
		return h.tagPrefix + uuid
	}
	return userTag(h.tag, uuid)
}
