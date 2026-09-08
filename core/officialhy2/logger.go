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
	tagPrefix            string
	limiter              *limiter.Limiter
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
	if ce := l.logger.Check(zap.DebugLevel, "TCP request"); ce != nil {
		ce.Write(zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
	}
}

func (l *eventLogger) TCPError(addr net.Addr, uuid, reqAddr string, err error) {
	if err == nil {
		if ce := l.logger.Check(zap.DebugLevel, "TCP closed"); ce != nil {
			ce.Write(zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
		}
		return
	}
	if ce := l.logger.Check(zap.DebugLevel, "TCP error"); ce != nil {
		ce.Write(zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr), zap.Error(err))
	}
}

func (l *eventLogger) UDPRequest(addr net.Addr, uuid string, sessionID uint32, reqAddr string) {
	l.checkLimit(addr, uuid)
	if ce := l.logger.Check(zap.DebugLevel, "UDP request"); ce != nil {
		ce.Write(zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionID), zap.String("reqAddr", reqAddr))
	}
}

func (l *eventLogger) UDPError(addr net.Addr, uuid string, sessionID uint32, err error) {
	if err == nil {
		if ce := l.logger.Check(zap.DebugLevel, "UDP closed"); ce != nil {
			ce.Write(zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionID))
		}
		return
	}
	if ce := l.logger.Check(zap.DebugLevel, "UDP error"); ce != nil {
		ce.Write(zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionID), zap.Error(err))
	}
}

func (l *eventLogger) checkLimit(addr net.Addr, uuid string) {
	ip := extractIPFromAddr(addr)
	taguuid := l.userTag(uuid)
	cacheKey := taguuid + "|" + ip + "|" + addr.Network()
	now := time.Now()
	if cached, ok := l.limitCache.Load(cacheKey); ok {
		entry := cached.(limitCacheEntry)
		if now.Before(entry.expiresAt) {
			if entry.reject {
				l.setUserOverLimit(taguuid, true)
			}
			return
		}
		l.limitCache.Delete(cacheKey)
	}

	limiterInfo, err := l.getLimiter()
	if err != nil {
		l.logger.Error("get limiter error", zap.String("tag", l.tag), zap.Error(err))
		return
	}
	_, reject := limiterInfo.CheckLimit(taguuid, ip, addr.Network() == "tcp")
	l.limitCache.Store(cacheKey, limitCacheEntry{
		reject:    reject,
		expiresAt: now.Add(l.limitCheckCacheTTL),
	})
	setLimiterOverLimit(limiterInfo, taguuid, reject)
	l.sweepLimitCache(now)
}

func (l *eventLogger) setUserOverLimit(taguuid string, reject bool) {
	limiterInfo, err := l.getLimiter()
	if err != nil {
		l.logger.Error("get limiter error", zap.String("tag", l.tag), zap.Error(err))
		return
	}
	setLimiterOverLimit(limiterInfo, taguuid, reject)
}

func (l *eventLogger) getLimiter() (*limiter.Limiter, error) {
	if l.limiter != nil {
		return l.limiter, nil
	}
	return limiter.GetLimiter(l.tag)
}

func (l *eventLogger) userTag(uuid string) string {
	if l.tagPrefix != "" {
		return l.tagPrefix + uuid
	}
	return userTag(l.tag, uuid)
}

func setLimiterOverLimit(limiterInfo *limiter.Limiter, taguuid string, reject bool) {
	if userLimit, ok := limiterInfo.UserLimitInfo.Load(taguuid); ok {
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
