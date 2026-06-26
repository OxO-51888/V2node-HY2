package core

import (
	"sync"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/OxO-51888/V2node-HY2/core/officialhy2"
	log "github.com/sirupsen/logrus"
)

type AddUsersParams struct {
	Tag   string
	Users []panel.UserInfo
	*panel.NodeInfo
}

type V2Core struct {
	Config   *conf.Conf
	ReloadCh chan struct{}
	access   sync.Mutex
	hy2      *officialhy2.Manager
	users    *UserMap
}

type UserMap struct {
	uidMap  map[string]int
	mapLock sync.RWMutex
}

func New(config *conf.Conf) *V2Core {
	hy2, err := officialhy2.New(&config.Unlock)
	if err != nil {
		log.WithField("err", err).Panic("failed to initialize official hysteria2 core")
	}
	core := &V2Core{
		Config: config,
		hy2:    hy2,
		users: &UserMap{
			uidMap: make(map[string]int),
		},
	}
	return core
}

func (v *V2Core) Start(infos []*panel.NodeInfo) error {
	v.access.Lock()
	defer v.access.Unlock()
	log.WithField("nodes", len(infos)).Info("HY2-only core started")
	return nil
}

func (v *V2Core) Close() error {
	v.access.Lock()
	defer v.access.Unlock()
	v.Config = nil
	if v.hy2 != nil {
		if err := v.hy2.Close(); err != nil {
			return err
		}
	}
	return nil
}
