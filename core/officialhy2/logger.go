package officialhy2

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OxO-51888/V2node-HY2/limiter"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultLimitCheckCacheTTL   = 2 * time.Second
	defaultLimitCacheSweepEvery = time.Minute
)

type eventLogger struct {
	tag                  string
	logger               *zap.Logger
	limitCache           sync.Map
	lastLimitCacheSweep  atomic.Int64
	limitCheckCacheTTL   time.Duration
	limitCacheSweepEvery time.Duration
}

type limitCacheEntry struct {
	reject    bool
	expiresAt time.Time
}

func (l *eventLogger) Connect(addr net.Addr, uuid string, tx uint64) {
	l.checkLimit(addr, uuid)
	l.logger.Info("client connected", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint64("tx", tx))
}

func (l *eventLogger) Disconnect(addr net.Addr, uuid string, err error) {
	l.logger.Info("client disconnected", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Error(err))
}

func (l *eventLogger) TCPRequest(addr net.Addr, uuid, reqAddr string) {
	l.checkLimit(addr, uuid)
	l.logger.Debug("TCP request", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
}

func (l *eventLogger) TCPError(addr net.Addr, uuid, reqAddr string, err error) {
	if err == nil {
		l.logger.Debug("TCP closed", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
		return
	}
	l.logger.Debug("TCP error", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr), zap.Error(err))
}

func (l *eventLogger) UDPRequest(addr net.Addr, uuid string, sessionID uint32, reqAddr string) {
	l.checkLimit(addr, uuid)
	l.logger.Debug("UDP request", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionID), zap.String("reqAddr", reqAddr))
}

func (l *eventLogger) UDPError(addr net.Addr, uuid string, sessionID uint32, err error) {
	if err == nil {
		l.logger.Debug("UDP closed", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionID))
		return
	}
	l.logger.Debug("UDP error", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionID), zap.Error(err))
}

func (l *eventLogger) checkLimit(addr net.Addr, uuid string) {
	ip := extractIPFromAddr(addr)
	cacheKey := uuid + "|" + ip + "|" + addr.Network()
	now := time.Now()
	if cached, ok := l.limitCache.Load(cacheKey); ok {
		entry := cached.(limitCacheEntry)
		if now.Before(entry.expiresAt) {
			if entry.reject {
				l.setUserOverLimit(uuid, true)
			}
			return
		}
		l.limitCache.Delete(cacheKey)
	}

	limiterInfo, err := limiter.GetLimiter(l.tag)
	if err != nil {
		l.logger.Error("get limiter error", zap.String("tag", l.tag), zap.Error(err))
		return
	}
	_, reject := limiterInfo.CheckLimit(userTag(l.tag, uuid), ip, addr.Network() == "tcp")
	l.limitCache.Store(cacheKey, limitCacheEntry{
		reject:    reject,
		expiresAt: now.Add(l.limitCheckCacheTTL),
	})
	setLimiterOverLimit(limiterInfo, l.tag, uuid, reject)
	l.sweepLimitCache(now)
}

func (l *eventLogger) setUserOverLimit(uuid string, reject bool) {
	limiterInfo, err := limiter.GetLimiter(l.tag)
	if err != nil {
		l.logger.Error("get limiter error", zap.String("tag", l.tag), zap.Error(err))
		return
	}
	setLimiterOverLimit(limiterInfo, l.tag, uuid, reject)
}

func setLimiterOverLimit(limiterInfo *limiter.Limiter, tag, uuid string, reject bool) {
	if userLimit, ok := limiterInfo.UserLimitInfo.Load(userTag(tag, uuid)); ok {
		userLimit.(*limiter.UserLimitInfo).OverLimit = reject
	}
}

func (l *eventLogger) sweepLimitCache(now time.Time) {
	last := l.lastLimitCacheSweep.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < l.limitCacheSweepEvery {
		return
	}
	if !l.lastLimitCacheSweep.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	l.limitCache.Range(func(key, value interface{}) bool {
		if now.After(value.(limitCacheEntry).expiresAt) {
			l.limitCache.Delete(key)
		}
		return true
	})
}

func newLogger(level string) (*zap.Logger, error) {
	zapLevel, ok := map[string]zapcore.Level{
		"debug": zapcore.DebugLevel,
		"info":  zapcore.InfoLevel,
		"warn":  zapcore.WarnLevel,
		"error": zapcore.ErrorLevel,
	}[strings.ToLower(level)]
	if !ok {
		return nil, fmt.Errorf("unsupported log level: %s", level)
	}
	return zap.Config{
		Level:             zap.NewAtomicLevelAt(zapLevel),
		DisableCaller:     true,
		DisableStacktrace: true,
		Encoding:          "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			MessageKey:     "msg",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeTime:     zapcore.RFC3339TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
		},
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}.Build()
}

func extractIPFromAddr(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.TCPAddr:
		return v.IP.String()
	case *net.UDPAddr:
		return v.IP.String()
	case *net.IPAddr:
		return v.IP.String()
	default:
		return ""
	}
}
