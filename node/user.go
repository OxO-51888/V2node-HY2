package node

import (
	"context"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	log "github.com/sirupsen/logrus"
)

func (c *Controller) reportUserTrafficTask(ctx context.Context) (err error) {
	var reportmin = 0
	var devicemin = 0
	if c.info != nil && c.info.Common != nil && c.info.Common.BaseConfig != nil {
		reportmin = c.info.Common.BaseConfig.NodeReportMinTraffic
		devicemin = c.info.Common.BaseConfig.DeviceOnlineMinTraffic
	}
	userTraffic, _ := c.server.GetUserTrafficSlice(c.tag, reportmin)
	stepCtx, cancel := panelRequestContext(ctx)
	err = c.apiClient.ReportUserTraffic(stepCtx, userTraffic)
	cancel()
	if err != nil {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Info("Report user traffic failed")
		if isPanelTimeout(err) {
			return nil
		}
	} else {
		if len(userTraffic) > 0 {
			c.server.CommitUserTraffic(c.tag, userTraffic)
			log.WithField("tag", c.tag).Infof("Report %d users traffic", len(userTraffic))
			//log.WithField("tag", c.tag).Debugf("User traffic: %+v", userTraffic)
		} else {
			log.WithField("tag", c.tag).Debug("Report empty traffic heartbeat")
		}
	}

	if !hasPanelTaskBudget(ctx) {
		log.WithField("tag", c.tag).Warn("Skip online device report because panel task budget is low")
		return nil
	}

	if onlineDevice, err := c.limiter.GetOnlineDevice(); err != nil {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Info("Get online device failed")
	} else {
		var result []panel.OnlineUser
		var nocountUID = make(map[int]struct{})
		totalOnline := 0
		for _, traffic := range userTraffic {
			total := traffic.Upload + traffic.Download
			if total < int64(devicemin*1000) {
				nocountUID[traffic.UID] = struct{}{}
			}
		}
		if onlineDevice != nil {
			totalOnline = len(*onlineDevice)
			for _, online := range *onlineDevice {
				if _, ok := nocountUID[online.UID]; !ok {
					result = append(result, online)
				}
			}
		}
		data := make(map[int][]string)
		for _, onlineuser := range result {
			// json structure: { UID1:["ip1","ip2"],UID2:["ip3","ip4"] }
			data[onlineuser.UID] = append(data[onlineuser.UID], onlineuser.IP)
		}
		if !hasPanelTaskBudget(ctx) {
			log.WithField("tag", c.tag).Warn("Skip online users report because panel task budget is low")
			return nil
		}
		stepCtx, cancel := panelRequestContext(ctx)
		err := c.apiClient.ReportNodeOnlineUsers(stepCtx, &data)
		cancel()
		if err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Info("Report online users failed")
			if isPanelTimeout(err) {
				return nil
			}
		}
		log.WithField("tag", c.tag).Infof("Total %d online users, %d Reported", totalOnline, len(result))
	}

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
