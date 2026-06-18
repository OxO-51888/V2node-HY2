package officialhy2

import (
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

	defaultStreamReceiveWindow = 16777216
	defaultConnReceiveWindow   = 41943040
	defaultMaxIdleTimeout      = 90 * time.Second
	defaultMaxIncomingStreams  = 4096
	defaultUDPIdleTimeout      = 60 * time.Second
	defaultCertCheckInterval   = 5 * time.Second
	defaultMasqRoot            = "/etc/v2node/masq"
)

type certificateCache struct {
	certFile      string
	keyFile       string
	checkInterval time.Duration

	mu          sync.RWMutex
	cert        tls.Certificate
	certModTime time.Time
	keyModTime  time.Time
	certSize    int64
	keySize     int64
	lastCheck   time.Time
}

type masqHandlerLogWrapper struct {
	handler http.Handler
	quic    bool
	logger  *zap.Logger
}

type staticMasqHandler struct {
	site        string
	content     []byte
	contentType string
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
		TLSConfig:  *tlsConfig,
		QUICConfig: *quicConfig,
		Conn:       conn,
		Outbound:   outbound,
		CongestionConfig: server.CongestionConfig{
			Type:       "bbr",
			BBRProfile: "aggressive",
		},
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
		certCache, err := newCertificateCache(certInfo.CertFile, certInfo.KeyFile, defaultCertCheckInterval)
		if err != nil {
			return nil, err
		}
		return &server.TLSConfig{
			Certificates:   []tls.Certificate{certCache.current()},
			GetCertificate: certCache.GetCertificate,
		}, nil
	}
}

func newCertificateCache(certFile, keyFile string, checkInterval time.Duration) (*certificateCache, error) {
	cache := &certificateCache{
		certFile:      certFile,
		keyFile:       keyFile,
		checkInterval: checkInterval,
	}
	if err := cache.reloadLocked(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *certificateCache) current() tls.Certificate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cert
}

func (c *certificateCache) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	now := time.Now()
	c.mu.RLock()
	if now.Sub(c.lastCheck) < c.checkInterval {
		cert := c.cert
		c.mu.RUnlock()
		return &cert, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.lastCheck) >= c.checkInterval {
		if changed, err := c.changedLocked(); err != nil {
			c.lastCheck = now
		} else if changed {
			if err := c.reloadLocked(); err != nil {
				c.lastCheck = now
			}
		} else {
			c.lastCheck = now
		}
	}
	cert := c.cert
	return &cert, nil
}

func (c *certificateCache) changedLocked() (bool, error) {
	certStat, err := os.Stat(c.certFile)
	if err != nil {
		return false, err
	}
	keyStat, err := os.Stat(c.keyFile)
	if err != nil {
		return false, err
	}
	return !certStat.ModTime().Equal(c.certModTime) ||
		!keyStat.ModTime().Equal(c.keyModTime) ||
		certStat.Size() != c.certSize ||
		keyStat.Size() != c.keySize, nil
}

func (c *certificateCache) reloadLocked() error {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return err
	}
	certStat, err := os.Stat(c.certFile)
	if err != nil {
		return err
	}
	keyStat, err := os.Stat(c.keyFile)
	if err != nil {
		return err
	}
	c.cert = cert
	c.certModTime = certStat.ModTime()
	c.keyModTime = keyStat.ModTime()
	c.certSize = certStat.Size()
	c.keySize = keyStat.Size()
	c.lastCheck = time.Now()
	return nil
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
	handler := n.getStaticMasqHandler()
	return &masqHandlerLogWrapper{
		handler: handler,
		quic:    true,
		logger:  n.events.logger,
	}
}

func (n *Node) getStaticMasqHandler() http.Handler {
	site := n.masqSite()
	if site == "" {
		return defaultMasqHandler("v2node")
	}
	indexPath := filepath.Join(defaultMasqRoot, site, "index.html")
	content, err := os.ReadFile(indexPath)
	if err != nil || len(content) == 0 {
		n.events.logger.Warn("masquerade page not found",
			zap.String("tag", n.tag),
			zap.String("site", site),
			zap.String("path", indexPath),
			zap.Error(err))
		return defaultMasqHandler(site)
	}
	return &staticMasqHandler{
		site:        site,
		content:     content,
		contentType: "text/html; charset=utf-8",
	}
}

func (n *Node) masqSite() string {
	if n.info == nil || n.info.Common == nil {
		return ""
	}
	serverName := ""
	if n.info.Common.CertInfo != nil {
		serverName = strings.ToLower(n.info.Common.CertInfo.CertDomain)
	}
	if serverName == "" {
		serverName = strings.ToLower(n.info.Common.TlsSettings.PrimaryServerName())
	}
	if site := masqSiteByName(serverName); site != "" {
		return site
	}
	return masqSiteByPort(n.info.Common.ServerPort)
}

func (h *staticMasqHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", h.contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write(h.content)
}

func defaultMasqHandler(site string) http.Handler {
	content := []byte("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" +
		html.EscapeString(site) +
		"</title></head><body></body></html>")
	return &staticMasqHandler{
		site:        site,
		content:     content,
		contentType: "text/html; charset=utf-8",
	}
}

func masqSiteByPort(port int) string {
	switch port {
	case 51801:
		return "gm"
	case 51802:
		return "nnm"
	case 51803:
		return "ovo"
	case 51804:
		return "yiyuan"
	case 51805:
		return "clash"
	case 51806:
		return "pianyi"
	default:
		return ""
	}
}

func masqSiteByName(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(name)), ".")
	if name == "" {
		return ""
	}
	firstLabel := strings.Split(name, ".")[0]
	switch firstLabel {
	case "gm", "nnm", "ovo", "yiyuan", "clash", "pianyi":
		return firstLabel
	}
	if port, err := strconv.Atoi(firstLabel); err == nil {
		return masqSiteByPort(port)
	}
	return ""
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
