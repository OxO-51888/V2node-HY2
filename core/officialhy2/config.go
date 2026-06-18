package officialhy2

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/correctnet"
	"github.com/apernet/hysteria/extras/v2/obfs"
	"github.com/apernet/hysteria/extras/v2/outbounds"
	"go.uber.org/zap"
)

const (
	byteSize     = 1
	kilobyteSize = byteSize * 1000
	megabyteSize = kilobyteSize * 1000

	defaultStreamReceiveWindow = 8388608
	defaultConnReceiveWindow   = defaultStreamReceiveWindow * 5 / 2
	defaultMaxIdleTimeout      = 30 * time.Second
	defaultMaxIncomingStreams  = 4096
	defaultUDPIdleTimeout      = 60 * time.Second
)

type masqHandlerLogWrapper struct {
	handler http.Handler
	quic    bool
	logger  *zap.Logger
}

func (m *masqHandlerLogWrapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.logger.Debug("masquerade request",
		zap.String("addr", r.RemoteAddr),
		zap.String("method", r.Method),
		zap.String("host", r.Host),
		zap.String("url", r.URL.String()),
		zap.Bool("quic", m.quic))
	m.handler.ServeHTTP(w, r)
}

func (n *Node) buildConfig(info *panel.NodeInfo) (*server.Config, error) {
	tlsConfig, err := n.getTLSConfig(info)
	if err != nil {
		return nil, err
	}
	quicConfig := n.getQUICConfig()
	conn, err := n.getConn(info)
	if err != nil {
		return nil, err
	}
	outbound := n.getOutboundConfig()
	masqHandler := n.getMasqHandler()

	return &server.Config{
		TLSConfig:             *tlsConfig,
		QUICConfig:            *quicConfig,
		Conn:                  conn,
		Outbound:              outbound,
		BandwidthConfig:       *n.getBandwidthConfig(info),
		IgnoreClientBandwidth: info.Common.Ignore_Client_Bandwidth,
		DisableUDP:            false,
		UDPIdleTimeout:        defaultUDPIdleTimeout,
		EventLogger:           n.events,
		TrafficLogger:         n.traffic,
		MasqHandler:           masqHandler,
	}, nil
}

func (n *Node) getTLSConfig(info *panel.NodeInfo) (*server.TLSConfig, error) {
	if info.Common == nil || info.Common.CertInfo == nil {
		return nil, fmt.Errorf("hysteria2 cert info is empty")
	}
	certInfo := info.Common.CertInfo
	switch certInfo.CertMode {
	case "none", "":
		return nil, fmt.Errorf("hysteria2 cert mode cannot be none")
	default:
		cert, err := tls.LoadX509KeyPair(certInfo.CertFile, certInfo.KeyFile)
		if err != nil {
			return nil, err
		}
		return &server.TLSConfig{
			Certificates: []tls.Certificate{cert},
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert, err := tls.LoadX509KeyPair(certInfo.CertFile, certInfo.KeyFile)
				return &cert, err
			},
		}, nil
	}
}

func (n *Node) getQUICConfig() *server.QUICConfig {
	return &server.QUICConfig{
		InitialStreamReceiveWindow:     defaultStreamReceiveWindow,
		MaxStreamReceiveWindow:         defaultStreamReceiveWindow,
		InitialConnectionReceiveWindow: defaultConnReceiveWindow,
		MaxConnectionReceiveWindow:     defaultConnReceiveWindow,
		MaxIdleTimeout:                 defaultMaxIdleTimeout,
		MaxIncomingStreams:             defaultMaxIncomingStreams,
		DisablePathMTUDiscovery:        false,
	}
}

func (n *Node) getConn(info *panel.NodeInfo) (net.PacketConn, error) {
	listenIP := ""
	serverPort := 0
	obfsType := ""
	obfsPassword := ""
	if info.Common != nil {
		listenIP = info.Common.ListenIP
		serverPort = info.Common.ServerPort
		obfsType = info.Common.Obfs
		obfsPassword = info.Common.ObfsPassword
	}
	uAddr, err := net.ResolveUDPAddr("udp", formatAddress(listenIP, serverPort))
	if err != nil {
		return nil, err
	}
	conn, err := correctnet.ListenUDP("udp", uAddr)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(obfsType) {
	case "", "plain":
		return conn, nil
	case "salamander":
		wrapped, err := obfs.WrapPacketConnSalamander(conn, []byte(obfsPassword))
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return wrapped, nil
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unsupported hysteria2 obfs type: %s", obfsType)
	}
}

func (n *Node) getBandwidthConfig(info *panel.NodeInfo) *server.BandwidthConfig {
	bandwidth := &server.BandwidthConfig{}
	if info.Common == nil {
		return bandwidth
	}
	if info.Common.UpMbps != 0 {
		bandwidth.MaxTx = uint64(info.Common.UpMbps * megabyteSize / 8)
	}
	if info.Common.DownMbps != 0 {
		bandwidth.MaxRx = uint64(info.Common.DownMbps * megabyteSize / 8)
	}
	return bandwidth
}

func (n *Node) getOutboundConfig() server.Outbound {
	return &outbounds.PluggableOutboundAdapter{
		PluggableOutbound: outbounds.NewDirectOutboundSimple(outbounds.DirectOutboundModeAuto),
	}
}

func (n *Node) getMasqHandler() http.Handler {
	return &masqHandlerLogWrapper{
		handler: http.NotFoundHandler(),
		quic:    true,
		logger:  n.events.logger,
	}
}

func formatAddress(ip string, port int) string {
	if ip == "" {
		return fmt.Sprintf(":%d", port)
	}
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}
