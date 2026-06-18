package officialhy2

import (
	"fmt"
	"net"
	"strings"
	"sync"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/counter"
	"github.com/OxO-51888/V2node-HY2/common/format"
	"github.com/apernet/hysteria/core/v2/server"
	log "github.com/sirupsen/logrus"
	"go.uber.org/zap"
)

type Manager struct {
	nodes  map[string]*Node
	logger *zap.Logger
	mu     sync.RWMutex
}

type Node struct {
	server  server.Server
	tag     string
	auth    *Authenticator
	events  *eventLogger
	traffic *trafficLogger
}

type Authenticator struct {
	users map[string]int
	mu    sync.RWMutex
}

func New() (*Manager, error) {
	logger, err := newLogger("error")
	if err != nil {
		return nil, err
	}
	return &Manager{
		nodes:  make(map[string]*Node),
		logger: logger,
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
		tag:  tag,
		auth: &Authenticator{users: make(map[string]int)},
		events: &eventLogger{
			tag:    tag,
			logger: m.logger,
		},
		traffic: &trafficLogger{
			tag:     tag,
			logger:  m.logger,
			counter: counter.NewTrafficCounter(),
		},
	}
	hyConfig, err := n.buildConfig(info)
	if err != nil {
		return err
	}
	hyConfig.Authenticator = n.auth
	s, err := server.NewServer(hyConfig)
	if err != nil {
		return err
	}
	n.server = s
	m.nodes[tag] = n

	go func() {
		if err := s.Serve(); err != nil && !strings.Contains(err.Error(), "quic: server closed") {
			log.WithFields(log.Fields{
				"tag": tag,
				"err": err,
			}).Error("official hysteria2 server error")
		}
	}()
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
	return n.server.Close()
}

func (m *Manager) Close() error {
	m.mu.Lock()
	nodes := m.nodes
	m.nodes = make(map[string]*Node)
	m.mu.Unlock()

	for tag, node := range nodes {
		if err := node.server.Close(); err != nil {
			return fmt.Errorf("close hysteria2 node %s: %w", tag, err)
		}
	}
	return nil
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
		traffic.UpCounter.Store(0)
		traffic.DownCounter.Store(0)

		uid := uidForUUID(uuid)
		if uid == 0 {
			n.traffic.counter.Delete(uuid)
			return true
		}
		trafficSlice = append(trafficSlice, panel.UserTraffic{
			UID:      uid,
			Upload:   up,
			Download: down,
		})
		return true
	})
	return trafficSlice
}

func userTag(tag, uuid string) string {
	return format.UserTag(tag, uuid)
}
