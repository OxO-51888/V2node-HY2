package server

import (
	"bytes"
	"io"
	"testing"
)

type readDiscardRW struct {
	*bytes.Reader
}

func (r *readDiscardRW) Write(p []byte) (int, error) {
	return len(p), nil
}

type allowTrafficLogger struct{}

func (allowTrafficLogger) LogTraffic(_ string, _, _ uint64) bool  { return true }
func (allowTrafficLogger) LogOnlineState(_ string, _ bool)        {}
func (allowTrafficLogger) TraceStream(_ HyStream, _ *StreamStats) {}
func (allowTrafficLogger) UntraceStream(_ HyStream)               {}

func TestCopyTwoWayExAllowsNilStats(t *testing.T) {
	serverSide := &readDiscardRW{Reader: bytes.NewReader([]byte("from client"))}
	remoteSide := &readDiscardRW{Reader: bytes.NewReader([]byte("from remote"))}
	if err := copyTwoWayEx("user", serverSide, remoteSide, allowTrafficLogger{}, nil); err != nil {
		t.Fatalf("copyTwoWayEx() error = %v", err)
	}
}

func BenchmarkCopyBufferLog(b *testing.B) {
	srcData := make([]byte, 1024*1024) // 1MB
	for i := range srcData {
		srcData[i] = byte(i)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		src := bytes.NewReader(srcData)
		dst := io.Discard
		copyBufferLog(dst, src, func(n uint64) bool { return true })
	}
}
