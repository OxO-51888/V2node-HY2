//go:build !linux

package officialhy2

import "net"

func setUDPReceiveBuffer(conn *net.UDPConn, requested int) (int, error) {
	// Effective buffer accounting is platform-specific outside Linux.
	return 0, conn.SetReadBuffer(requested)
}
