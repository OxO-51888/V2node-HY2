package officialhy2

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/correctnet"
	"github.com/apernet/hysteria/extras/v2/obfs"
	"github.com/apernet/hysteria/extras/v2/outbounds"
	"github.com/apernet/hysteria/extras/v2/outbounds/acl"
	log "github.com/sirupsen/logrus"
	"go.uber.org/zap"
)

const (
	byteSize     = 1
	kilobyteSize = byteSize * 1000
	megabyteSize = kilobyteSize * 1000

	defaultStreamReceiveWindow = 8388608
	defaultConnReceiveWindow   = defaultStreamReceiveWindow * 5 / 2
	defaultMaxIdleTimeout      = 30 * time.Second
	defaultMaxIncomingStreams  = 1024
	defaultUDPIdleTimeout      = 60 * time.Second
	defaultCertCheckInterval   = 5 * time.Second
	defaultMasqRoot            = "/etc/v2node/masq"
)

type certificateCache struct {
	certFile      string
	keyFile       string
	checkInterval time.Duration

	mu              sync.RWMutex
	cert            tls.Certificate
	certInfo        os.FileInfo
	keyInfo         os.FileInfo
	lastCheck       time.Time
	lastReloadWarn  time.Time
	lastExpiryWarn  time.Time
	expiryWarnedFor time.Time
}

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
	bandwidth, err := n.getBandwidthConfig(info)
	if err != nil {
		return nil, err
	}
	outbound, err := n.getOutboundConfig()
	if err != nil {
		return nil, err
	}
	conn, err := n.getConn(info)
	if err != nil {
		return nil, err
	}
	masqHandler := n.getMasqHandler()

	return &server.Config{
		TLSConfig:  *tlsConfig,
		QUICConfig: *quicConfig,
		Conn:       conn,
		Outbound:   outbound,
		CongestionConfig: server.CongestionConfig{
			Type: "bbr",
		},
		BandwidthConfig:       *bandwidth,
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
	cache.checkExpiryLocked(time.Now())
	return cache, nil
}

func (c *certificateCache) checkExpiryLocked(now time.Time) {
	exp := c.cert.Leaf.NotAfter
	if exp.Sub(now) <= 14*24*time.Hour && (exp != c.expiryWarnedFor || now.Sub(c.lastExpiryWarn) >= 24*time.Hour) {
		c.lastExpiryWarn, c.expiryWarnedFor = now, exp
		log.WithFields(log.Fields{"cert_file": c.certFile, "not_after": exp.UTC()}).Warn("Serving TLS certificate near or past expiry")
	}
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
		changed, err := c.changedLocked()
		if err == nil && changed {
			err = c.reloadLocked()
		}
		if err != nil && (c.lastReloadWarn.IsZero() || now.Sub(c.lastReloadWarn) >= 10*time.Minute) {
			log.WithFields(log.Fields{"cert_file": c.certFile, "not_after": c.cert.Leaf.NotAfter.UTC()}).WithError(err).Warn("TLS certificate refresh failed; retaining serving certificate")
			c.lastReloadWarn = now
		} else if err == nil && !c.lastReloadWarn.IsZero() {
			log.WithField("cert_file", c.certFile).Info("TLS certificate refresh healthy")
			c.lastReloadWarn = time.Time{}
		}
		c.checkExpiryLocked(now)
		c.lastCheck = now
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
	return !sameCertFile(certStat, c.certInfo) || !sameCertFile(keyStat, c.keyInfo), nil
}

func sameCertFile(a, b os.FileInfo) bool {
	return b != nil && os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size()
}

func (c *certificateCache) reloadLocked() error {
	certStat, err := os.Stat(c.certFile)
	if err != nil {
		return err
	}
	keyStat, err := os.Stat(c.keyFile)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return err
	}
	if cert.Leaf == nil {
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return err
		}
	}
	// Parse success only proves that the key matches, not that the replacement
	// is usable now. Keep the current certificate until the whole supplied
	// chain is within its validity period.
	now := time.Now()
	for i, der := range cert.Certificate {
		part := cert.Leaf
		if i > 0 {
			part, err = x509.ParseCertificate(der)
			if err != nil {
				return fmt.Errorf("parse certificate chain entry %d: %w", i, err)
			}
		}
		if now.Before(part.NotBefore) || !now.Before(part.NotAfter) {
			return fmt.Errorf("certificate chain entry %d is not currently valid (not_before=%s not_after=%s)", i, part.NotBefore.UTC().Format(time.RFC3339), part.NotAfter.UTC().Format(time.RFC3339))
		}
	}
	c.cert = cert
	c.certInfo, c.keyInfo = certStat, keyStat
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
		listenIP = normalizeListenIP(info.Common.ListenIP)
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
	prepareUDPReceiveBuffer(conn)

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

func (n *Node) getBandwidthConfig(info *panel.NodeInfo) (*server.BandwidthConfig, error) {
	const key = "V2NODE_FORCE_SERVER_BANDWIDTH"
	force := true
	if raw, ok := os.LookupEnv(key); ok {
		v, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s must be a boolean: %w", key, err)
		}
		force = v
	}
	bandwidth := &server.BandwidthConfig{ForceServerBandwidth: force}
	if info.Common == nil {
		return bandwidth, nil
	}
	if info.Common.UpMbps != 0 {
		bandwidth.MaxTx = uint64(info.Common.UpMbps * megabyteSize / 8)
	}
	if info.Common.DownMbps != 0 {
		bandwidth.MaxRx = uint64(info.Common.DownMbps * megabyteSize / 8)
	}
	return bandwidth, nil
}

func (n *Node) getOutboundConfig() (server.Outbound, error) {
	if n.unlock == nil || !n.unlock.Enable || len(n.unlock.SOCKS) == 0 {
		return nil, nil
	}

	direct := newFastDirectOutbound()
	pluggable := direct

	unlockOutbound, entries := n.getUnlockOutbounds(direct)
	if unlockOutbound != "" {
		rules := n.getUnlockRules(unlockOutbound)
		if rules != "" {
			translated, err := acl.TranslateText(rules, "unlock rules")
			if err != nil {
				return nil, fmt.Errorf("translate unlock acl: %w", err)
			}
			aclOutbound, err := outbounds.NewACLEngine(translated, entries, nil)
			if err != nil {
				return nil, fmt.Errorf("build unlock acl: %w", err)
			}
			pluggable = aclOutbound
		}
	}

	return &fastOutboundAdapter{outbound: pluggable}, nil
}

func (n *Node) getUnlockOutbounds(direct outbounds.PluggableOutbound) (string, []outbounds.OutboundEntry) {
	entries := []outbounds.OutboundEntry{
		{Name: "direct", Outbound: direct},
		{Name: "default", Outbound: direct},
	}
	targetTag := strings.ToLower(strings.TrimSpace(n.unlock.DefaultOutbound))
	firstTag := ""
	for _, item := range n.unlock.SOCKS {
		tag := strings.ToLower(strings.TrimSpace(item.Tag))
		if tag == "" || item.Address == "" || item.Port <= 0 {
			continue
		}
		if firstTag == "" {
			firstTag = tag
		}
		entries = append(entries, outbounds.OutboundEntry{
			Name: tag,
			Outbound: outbounds.NewSOCKS5Outbound(
				formatAddress(item.Address, item.Port),
				item.Username,
				item.Password,
			),
		})
	}
	if firstTag == "" {
		return "", entries
	}
	if targetTag == "" {
		targetTag = firstTag
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, targetTag) && targetTag != "direct" && targetTag != "default" {
			return targetTag, entries
		}
	}
	return firstTag, entries
}

func (n *Node) getUnlockRules(defaultOutbound string) string {
	var lines []string
	if len(n.unlock.RuleSets) > 0 {
		for tag, rules := range n.unlock.RuleSets {
			outbound := strings.ToLower(strings.TrimSpace(tag))
			if outbound == "" {
				outbound = defaultOutbound
			}
			for _, rule := range rules {
				if line := aclRuleLine(outbound, rule); line != "" {
					lines = append(lines, line)
				}
			}
		}
	}
	for _, rule := range n.unlock.Rules {
		if line := aclRuleLine(defaultOutbound, rule); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		for _, domain := range defaultUnlockDomains {
			if line := aclRuleLine(defaultOutbound, domain); line != "" {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func aclRuleLine(outbound, value string) string {
	outbound = strings.ToLower(strings.TrimSpace(outbound))
	value = strings.TrimSpace(value)
	if outbound == "" || value == "" {
		return ""
	}
	if strings.Contains(value, "(") && strings.HasSuffix(value, ")") {
		return value
	}
	addr := value
	if !strings.Contains(addr, ":") && !strings.Contains(addr, "/") && net.ParseIP(addr) == nil {
		addr = "suffix:" + strings.TrimPrefix(addr, ".")
	}
	return fmt.Sprintf("%s(%s)", outbound, addr)
}

var defaultUnlockDomains = []string{
	"netflix.com",
	"netflix.net",
	"nflxext.com",
	"nflximg.net",
	"nflxso.net",
	"nflxvideo.net",
	"disneyplus.com",
	"disney-plus.net",
	"dssott.com",
	"bamgrid.com",
	"max.com",
	"hbomax.com",
	"hbomaxcdn.com",
	"hulu.com",
	"huluim.com",
	"primevideo.com",
	"amazonvideo.com",
	"media-amazon.com",
	"tv.apple.com",
	"itunes.apple.com",
}

func (n *Node) getMasqHandler() http.Handler {
	return &masqHandlerLogWrapper{
		handler: sitePageHandler{root: defaultMasqRoot, site: n.masqSite()},
		quic:    true,
		logger:  n.events.logger,
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
	ip = trimBracketedIP(strings.TrimSpace(ip))
	if ip == "" {
		return fmt.Sprintf(":%d", port)
	}
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

func normalizeListenIP(ip string) string {
	ip = trimBracketedIP(strings.TrimSpace(ip))
	switch strings.ToLower(ip) {
	case "", "*", "0.0.0.0", "::":
		return ""
	default:
		return ip
	}
}

func trimBracketedIP(ip string) string {
	if strings.HasPrefix(ip, "[") && strings.HasSuffix(ip, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(ip, "["), "]")
	}
	return ip
}
