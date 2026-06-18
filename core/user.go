package core

import (
	"fmt"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/format"
)

func (vc *V2Core) DelUsers(users []panel.UserInfo, tag string, _ *panel.NodeInfo) error {
	vc.users.mapLock.Lock()
	defer vc.users.mapLock.Unlock()

	if vc.hy2.HasNode(tag) {
		if err := vc.hy2.DelUsers(tag, users); err != nil {
			return err
		}
	}
	for i := range users {
		delete(vc.users.uidMap, format.UserTag(tag, users[i].Uuid))
	}
	return nil
}

func (vc *V2Core) GetUserTrafficSlice(tag string, mintraffic int) ([]panel.UserTraffic, error) {
	vc.users.mapLock.RLock()
	defer vc.users.mapLock.RUnlock()

	if !vc.hy2.HasNode(tag) {
		return nil, nil
	}
	trafficSlice := vc.hy2.GetUserTrafficSlice(tag, mintraffic, func(uuid string) int {
		return vc.users.uidMap[format.UserTag(tag, uuid)]
	})
	if len(trafficSlice) == 0 {
		return nil, nil
	}
	return trafficSlice, nil
}

func (v *V2Core) AddUsers(p *AddUsersParams) (added int, err error) {
	if p == nil || p.NodeInfo == nil {
		return 0, fmt.Errorf("empty add users params")
	}
	if p.NodeInfo.Type != "hysteria2" {
		return 0, fmt.Errorf("HY2-only backend does not support node type: %s", p.NodeInfo.Type)
	}

	v.users.mapLock.Lock()
	defer v.users.mapLock.Unlock()

	for i := range p.Users {
		v.users.uidMap[format.UserTag(p.Tag, p.Users[i].Uuid)] = p.Users[i].Id
	}
	return v.hy2.AddUsers(p.Tag, p.Users)
}
