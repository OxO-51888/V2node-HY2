package officialhy2

import (
	"fmt"
	"net"
	"strings"

	"github.com/OxO-51888/V2node-HY2/limiter"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type eventLogger struct {
	tag    string
	logger *zap.Logger
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
	limiterInfo, err := limiter.GetLimiter(l.tag)
	if err != nil {
		l.logger.Error("get limiter error", zap.String("tag", l.tag), zap.Error(err))
		return
	}
	_, reject := limiterInfo.CheckLimit(userTag(l.tag, uuid), extractIPFromAddr(addr), addr.Network() == "tcp")
	if userLimit, ok := limiterInfo.UserLimitInfo.Load(userTag(l.tag, uuid)); ok {
		userLimit.(*limiter.UserLimitInfo).OverLimit = reject
	}
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
