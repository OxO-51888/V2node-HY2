package officialhy2

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func udpReceiveBufferSize(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var size int
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		size, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
	})
	if err != nil {
		return 0, err
	}
	return size, sockErr
}

func setUDPReceiveBuffer(conn *net.UDPConn, requested int) (int, error) {
	before, err := udpReceiveBufferSize(conn)
	if err != nil {
		return 0, err
	}
	target := requested * 2
	if before >= target {
		return before, nil
	}
	actual := before
	// A lowered system cap must not shrink an already larger live buffer.
	if data, readErr := os.ReadFile("/proc/sys/net/core/rmem_max"); readErr == nil {
		capBytes, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && capBytes > 0 && min(requested, capBytes)*2 > before {
			if err := conn.SetReadBuffer(requested); err != nil {
				return before, err
			}
			actual, err = udpReceiveBufferSize(conn)
			if err != nil || actual >= target {
				return actual, err
			}
		}
	}

	// Raise only this socket's cap when permitted; never change global sysctls.
	raw, err := conn.SyscallConn()
	if err != nil {
		return actual, err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, requested)
	}); err != nil {
		return actual, err
	}
	if sockErr != nil {
		return actual, fmt.Errorf("receive buffer is %d bytes, target %d: %w", actual, target, sockErr)
	}
	actual, err = udpReceiveBufferSize(conn)
	if err == nil && actual < target {
		err = fmt.Errorf("receive buffer is %d bytes, target %d", actual, target)
	}
	return actual, err
}
