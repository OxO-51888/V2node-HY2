package officialhy2

import (
	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/limiter"
	"github.com/apernet/hysteria/core/v2/server"
	"go.uber.org/zap"
)

type trafficLogger struct {
	tag     string
	logger  *zap.Logger
	counter *counter.TrafficCounter
}

func (h *trafficLogger) LogTraffic(id string, tx, rx uint64) bool {
	limiterInfo, err := limiter.GetLimiter(h.tag)
	if err != nil {
		h.logger.Error("get limiter error", zap.String("tag", h.tag), zap.Error(err))
		return false
	}

	if userLimit, ok := limiterInfo.UserLimitInfo.Load(userTag(h.tag, id)); ok {
		userLimitInfo := userLimit.(*limiter.UserLimitInfo)
		if userLimitInfo.OverLimit {
			userLimitInfo.OverLimit = false
			return false
		}
	}

	h.counter.Rx(id, int(rx))
	h.counter.Tx(id, int(tx))
	return true
}

func (h *trafficLogger) LogOnlineState(_ string, _ bool) {}

func (h *trafficLogger) TraceStream(_ server.HyStream, _ *server.StreamStats) {}

func (h *trafficLogger) UntraceStream(_ server.HyStream) {}
