package node

import (
	"context"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	log "github.com/sirupsen/logrus"
)

func (c *Controller) reportUserTrafficTask(ctx context.Context) error {
	info := c.currentInfo()
	reportMin, deviceMin := 0, 0
	if info != nil && info.Common != nil && info.Common.BaseConfig != nil {
		reportMin = info.Common.BaseConfig.NodeReportMinTraffic
		deviceMin = info.Common.BaseConfig.DeviceOnlineMinTraffic
	}
	var traffic []panel.UserTraffic
	stepCtx, cancel := panelRequestContext(ctx)
	defer cancel()
	err := c.apiClient.FlushTrafficLimit(stepCtx, func() ([]panel.UserTraffic, error) {
		var err error
		traffic, err = c.server.GetUserTrafficSlice(c.tag, reportMin)
		if err == nil {
			small := map[int]bool{}
			for _, row := range traffic {
				if row.Upload+row.Download < int64(deviceMin)*1000 {
					small[row.UID] = true
				}
			}
			c.onlineTrafficAccess.Lock()
			c.onlineSmallTraffic = small
			c.onlineTrafficAccess.Unlock()
		}
		return traffic, err
	}, func(rows []panel.UserTraffic) error {
		return c.server.CommitUserTrafficSlice(c.tag, rows)
	}, c.revokeReportedUsers, 200)
	if err != nil {
		log.WithFields(log.Fields{"tag": c.tag, "err": err}).Warn("Traffic unconfirmed; retain pending batch and retry")
	}
	return nil
}

func (c *Controller) reportHeartbeat(ctx context.Context) error {
	devices, err := c.limiter.GetOnlineDevice()
	if err != nil {
		log.WithError(err).Warn("Read online devices failed")
		return nil
	}
	c.onlineTrafficAccess.RLock()
	small := c.onlineSmallTraffic
	c.onlineTrafficAccess.RUnlock()
	data := map[int][]string{}
	if devices != nil {
		for _, d := range *devices {
			if !small[d.UID] {
				data[d.UID] = append(data[d.UID], d.IP)
			}
		}
	}
	stepCtx, cancel := panelRequestContext(ctx)
	err = c.apiClient.ReportNodeOnlineUsers(stepCtx, &data)
	cancel()
	if err != nil {
		log.WithFields(log.Fields{"tag": c.tag, "err": err}).Warn("Online heartbeat unconfirmed")
	}
	if hasPanelTaskBudget(ctx) {
		stepCtx, cancel := panelRequestContext(ctx)
		alive, err := c.apiClient.GetUserAlive(stepCtx)
		cancel()
		if err == nil && alive != nil {
			c.limiter.UpdateAliveList(alive)
		}
	}
	// A transient reporting/storage failure must not terminate the recurring task.
	return nil
}

func (c *Controller) revokeReportedUsers(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	c.syncUsersAccess.Lock()
	defer c.syncUsersAccess.Unlock()
	c.userGeneration++
	revoked := make(map[int]bool, len(ids))
	for _, id := range ids {
		revoked[id] = true
	}
	kept := make([]panel.UserInfo, 0, len(c.userList))
	deleted := make([]panel.UserInfo, 0, len(ids))
	for _, u := range c.userList {
		if revoked[u.Id] {
			deleted = append(deleted, u)
		} else {
			kept = append(kept, u)
		}
	}
	if err := c.server.DelUsers(deleted, c.tag, c.currentInfo()); err != nil {
		return err
	}
	c.limiter.UpdateUser(c.tag, nil, deleted, nil)
	c.userList = kept
	c.apiClient.InvalidateUsers()
	return nil
}

func compareUserList(old, new []panel.UserInfo) (deleted, added, modified []panel.UserInfo) {
	oldMap := make(map[string]panel.UserInfo, len(old))
	for _, u := range old {
		oldMap[u.Uuid] = u
	}
	for _, u := range new {
		if o, ok := oldMap[u.Uuid]; !ok {
			added = append(added, u)
		} else {
			if o.SpeedLimit != u.SpeedLimit || o.DeviceLimit != u.DeviceLimit {
				modified = append(modified, u)
			}
			delete(oldMap, u.Uuid)
		}
	}
	for _, o := range oldMap {
		deleted = append(deleted, o)
	}
	return deleted, added, modified
}
