package officialhy2

import (
	"net"

	log "github.com/sirupsen/logrus"
)

// Linux accounts for socket overhead by reporting twice this requested size.
const udpReceiveBufferRequest = 32 * 1024 * 1024

func prepareUDPReceiveBuffer(conn *net.UDPConn) {
	actual, err := setUDPReceiveBuffer(conn, udpReceiveBufferRequest)
	fields := log.Fields{
		"address":         conn.LocalAddr().String(),
		"requested_bytes": udpReceiveBufferRequest,
		"effective_bytes": actual,
	}
	if err != nil {
		log.WithFields(fields).WithError(err).Warn("HY2 UDP receive buffer below target; keeping listener available")
		return
	}
	log.WithFields(fields).Info("HY2 UDP receive buffer configured")
}
