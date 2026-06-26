package officialhy2

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/common/format"
	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/apernet/hysteria/core/v2/server"
	log "github.com/sirupsen/logrus"
	"go.uber.org/zap"
)

type Manager struct {
	nodes  map[string]*Node
	logger *zap.Logger
	unlock *conf.UnlockConfig
	mu     sync.RWMutex
}

type Node struct {
	server    server.Server
	tag       string
	info      *panel.NodeInfo
	auth      *Authenticator
	events    *eventLogger
	traffic   *trafficLogger
	unlock    *conf.UnlockConfig
	serverMu  sync.Mutex
	stopCh    chan struct{}
	serveDone chan struct{}
	stopOnce  sync.Once
}

type Authenticator struct {
	users map[string]int
	mu    sync.RWMutex
}

func New(unlock *conf.UnlockConfig) (*Manager, error) {
	logger, err := newLogger("error")
	if err != nil {
		return nil, err
	}
	return &Manager{
		nodes:  make(map[string]*Node),
		logger: logger,
		unlock: unlock,
	}, nil
}

func (a *Authenticator) Authenticate(_ net.Addr, auth string, _ uint64) (bool, string) {
	a.mu.RLock()
	_, ok := a.users[auth]
	a.mu.RUnlock()
	if !ok {
		return false, ""
	}
	return true, auth
}

func (m *Manager) HasNode(tag string) bool {
	m.mu.RLock()
	_, ok := m.nodes[tag]
	m.mu.RUnlock()
	return ok
}

func (m *Manager) AddNode(tag string, info *panel.NodeInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.nodes[tag]; exists {
		return fmt.Errorf("hysteria2 node %s already exists", tag)
	}

	n := &Node{
		tag:    tag,
		auth:   &Authenticator{users: make(map[string]int)},
		unlock: m.unlock,
		events: &eventLogger{
			tag:                  tag,
			logger:               m.logger,
			limitCheckCacheTTL:   defaultLimitCheckCacheTTL,
			limitCacheSweepEvery: defaultLimitCacheSweepEvery,
		},
		traffic: &trafficLogger{
			tag:     tag,
			logger:  m.logger,
			counter: counter.NewTrafficCounter(),
		},
		info:      info,
		stopCh:    make(chan struct{}),
		serveDone: make(chan struct{}),
	}
	s, err := n.newServer()
	if err != nil {
		return err
	}
	n.server = s
	m.nodes[tag] = n

	go n.serveLoop()
	return nil
}

func (m *Manager) DelNode(tag string) error {
	m.mu.Lock()
	n, ok := m.nodes[tag]
	if ok {
		delete(m.nodes, tag)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("hysteria2 node %s not found", tag)
	}
	return n.stop()
}

func (m *Manager) Close() error {
	m.mu.Lock()
	nodes := m.nodes
	m.nodes = make(map[string]*Node)
	m.mu.Unlock()

	var closeErr error
	for tag, node := range nodes {
		if err := node.stop(); err != nil {
			log.WithFields(log.Fields{
				"tag": tag,
				"err": err,
			}).Error("close hysteria2 node failed")
			if closeErr == nil {
				closeErr = fmt.Errorf("close hysteria2 node %s: %w", tag, err)
			}
		}
	}
	return closeErr
}

func (m *Manager) AddUsers(tag string, users []panel.UserInfo) (int, error) {
	m.mu.RLock()
	n, ok := m.nodes[tag]
	m.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("hysteria2 node %s not found", tag)
	}
	n.auth.mu.Lock()
	defer n.auth.mu.Unlock()
	for _, user := range users {
		n.auth.users[user.Uuid] = user.Id
	}
	return len(users), nil
}

func (m *Manager) DelUsers(tag string, users []panel.UserInfo) error {
	m.mu.RLock()
	n, ok := m.nodes[tag]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("hysteria2 node %s not found", tag)
	}
	n.auth.mu.Lock()
	defer n.auth.mu.Unlock()
	for _, user := range users {
		delete(n.auth.users, user.Uuid)
		n.traffic.counter.Delete(user.Uuid)
	}
	return nil
}

func (m *Manager) GetUserTrafficSlice(tag string, minTraffic int, uidForUUID func(uuid string) int) []panel.UserTraffic {
	m.mu.RLock()
	n, ok := m.nodes[tag]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	var trafficSlice []panel.UserTraffic
	n.traffic.counter.Counters.Range(func(key, value interface{}) bool {
		uuid := key.(string)
		traffic := value.(*counter.TrafficStorage)
		up := traffic.UpCounter.Load()
		down := traffic.DownCounter.Load()
		if up+down <= int64(minTraffic*1000) {
			return true
		}

		uid := uidForUUID(uuid)
		if uid == 0 {
			n.traffic.counter.Delete(uuid)
			return true
		}
		trafficSlice = append(trafficSlice, panel.UserTraffic{
			UID:      uid,
			Upload:   up,
			Download: down,
			UUID:     uuid,
		})
		return true
	})
	return trafficSlice
}

func (m *Manager) CommitUserTraffic(tag string, reported []panel.UserTraffic) {
	m.mu.RLock()
	n, ok := m.nodes[tag]
	m.mu.RUnlock()
	if !ok {
		return
	}
	for _, traffic := range reported {
		if traffic.UUID == "" {
			continue
		}
		n.traffic.counter.Subtract(traffic.UUID, traffic.Upload, traffic.Download)
	}
}

func userTag(tag, uuid string) string {
	return format.UserTag(tag, uuid)
}

func (n *Node) newServer() (server.Server, error) {
	hyConfig, err := n.buildConfig(n.info)
	if err != nil {
		return nil, err
	}
	hyConfig.Authenticator = n.auth
	return server.NewServer(hyConfig)
}

func (n *Node) serveLoop() {
	defer close(n.serveDone)

	backoff := time.Second
	for {
		n.serverMu.Lock()
		s := n.server
		n.serverMu.Unlock()
		if s == nil {
			return
		}

		err := s.Serve()
		select {
		case <-n.stopCh:
			return
		default:
		}

		if err != nil && !isServerClosedError(err) {
			log.WithFields(log.Fields{
				"tag": n.tag,
				"err": err,
			}).Error("official hysteria2 server stopped unexpectedly")
		} else {
			log.WithField("tag", n.tag).Warn("official hysteria2 server stopped unexpectedly")
		}

		_ = n.closeServer()
		if !n.waitBeforeRestart(backoff) {
			return
		}

		nextServer, err := n.newServer()
		if err != nil {
			log.WithFields(log.Fields{
				"tag":     n.tag,
				"err":     err,
				"backoff": backoff.String(),
			}).Error("restart official hysteria2 server failed")
			backoff = nextRestartBackoff(backoff)
			continue
		}
		n.serverMu.Lock()
		n.server = nextServer
		n.serverMu.Unlock()
		log.WithField("tag", n.tag).Warn("official hysteria2 server restarted")
		backoff = time.Second
	}
}

func (n *Node) waitBeforeRestart(backoff time.Duration) bool {
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-n.stopCh:
		return false
	}
}

func nextRestartBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func (n *Node) stop() error {
	var closeErr error
	n.stopOnce.Do(func() {
		close(n.stopCh)
		closeErr = n.closeServer()
		select {
		case <-n.serveDone:
		case <-time.After(5 * time.Second):
			log.WithField("tag", n.tag).Warn("timed out waiting for hysteria2 server goroutine to stop")
		}
	})
	return closeErr
}

func (n *Node) closeServer() error {
	n.serverMu.Lock()
	s := n.server
	n.server = nil
	n.serverMu.Unlock()
	if s == nil {
		return nil
	}
	if err := s.Close(); err != nil && !isServerClosedError(err) {
		return err
	}
	return nil
}

func isServerClosedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "quic: server closed")
}
