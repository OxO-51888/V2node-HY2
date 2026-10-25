package node

import (
	"context"
	"reflect"
	"strings"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/task"
	vCore "github.com/OxO-51888/V2node-HY2/core"
	log "github.com/sirupsen/logrus"
)

const minReloadInterval = time.Minute

func (c *Controller) startTasks(node *panel.NodeInfo) {
	// fetch node info task
	c.nodeInfoMonitorPeriodic = &task.Task{
		Name:               "nodeInfoMonitor",
		Interval:           node.PullInterval,
		Execute:            c.nodeInfoMonitor,
		ReloadCh:           c.server.ReloadCh,
		Timeout:            panelTaskTimeout,
		ExitOnTimeout:      true,
		ExitOnTimeoutAfter: 3,
	}
	// fetch user list task
	c.userReportPeriodic = &task.Task{
		Name:               "reportUserTrafficTask",
		Interval:           node.PushInterval,
		Execute:            c.reportUserTrafficTask,
		ReloadCh:           c.server.ReloadCh,
		Timeout:            panelTaskTimeout,
		ExitOnTimeout:      true,
		ExitOnTimeoutAfter: 3,
	}
	c.heartbeatPeriodic = &task.Task{
		Name: "onlineHeartbeat", Interval: node.PushInterval, Execute: c.reportHeartbeat,
		ReloadCh: c.server.ReloadCh, Timeout: panelTaskTimeout,
	}
	log.WithField("tag", c.tag).Info("Start monitor node status")
	// delay to start nodeInfoMonitor
	_ = c.nodeInfoMonitorPeriodic.Start(false)
	log.WithField("tag", c.tag).Info("Start report node status")
	_ = c.userReportPeriodic.Start(false)
	_ = c.heartbeatPeriodic.Start(false)
	if node.Security == panel.Tls {
		switch c.info.Common.CertInfo.CertMode {
		case "none", "", "file", "self", "remote":
		default:
			c.renewCertPeriodic = &task.Task{
				Name:     "renewCertTask",
				Interval: time.Hour * 24,
				Execute:  c.renewCertTask,
				ReloadCh: c.server.ReloadCh,
			}
			log.WithField("tag", c.tag).Info("Start renew cert")
			// delay to start renewCert
			_ = c.renewCertPeriodic.Start(true)
		}
	}
}

func (c *Controller) nodeInfoMonitor(ctx context.Context) (err error) {
	// get node info
	stepCtx, cancel := panelRequestContext(ctx)
	newN, err := c.apiClient.GetNodeInfo(stepCtx)
	cancel()
	if err != nil {
		if isPanelTimeout(err) {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Warn("Get node info timed out, skip this interval")
			return nil
		}
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Error("Get node info failed")
		return nil
	}
	if newN != nil {
		if requiresCoreReload(c.currentInfo(), newN) {
			log.WithFields(log.Fields{
				"tag":     c.tag,
				"changes": strings.Join(coreReloadReasons(c.currentInfo(), newN), ","),
			}).Warn("Got core node info change, reload")
			c.requestReload()
			return nil
		}
		c.applyRuntimeNodeInfo(newN)
		log.WithField("tag", c.tag).Info("Applied non-core node info change without reload")
	}
	log.WithField("tag", c.tag).Debug("Node info no change")

	return c.syncUsers(ctx)
}

func (c *Controller) syncUsers(ctx context.Context) (err error) {
	c.userFetchAccess.Lock()
	defer c.userFetchAccess.Unlock()
	c.syncUsersAccess.Lock()
	generation := c.userGeneration
	c.syncUsersAccess.Unlock()

	stepCtx, cancel := panelRequestContext(ctx)
	snapshot, err := c.apiClient.GetUsers(stepCtx)
	cancel()
	if err != nil {
		if isPanelTimeout(err) {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Warn("Get user list timed out, skip this interval")
			return nil
		}
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Error("Get user list failed")
		return nil
	}
	c.syncUsersAccess.Lock()
	defer c.syncUsersAccess.Unlock()
	if generation != c.userGeneration {
		c.apiClient.InvalidateUsers()
		return nil
	}
	// node no changed, check users
	if snapshot == nil {
		log.WithField("tag", c.tag).Debug("User list no change")
		return nil
	}
	newU := snapshot.Users
	deleted, added, modified := compareUserList(c.userList, newU)
	if len(deleted) > 0 {
		// have deleted users
		err = c.server.DelUsers(deleted, c.tag, c.currentInfo())
		if err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Delete users failed")
			return nil
		}
	}
	if len(added) > 0 {
		// have added users
		_, err = c.server.AddUsers(&vCore.AddUsersParams{
			Tag:      c.tag,
			NodeInfo: c.currentInfo(),
			Users:    added,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Add users failed")
			return nil
		}
	}
	if len(added) > 0 || len(deleted) > 0 || len(modified) > 0 {
		// update Limiter
		c.limiter.UpdateUser(c.tag, added, deleted, modified)
	}
	c.userList = newU
	c.apiClient.CommitUsers(snapshot)
	log.WithField("tag", c.tag).Infof("%d user deleted, %d user added, %d user modified", len(deleted), len(added), len(modified))
	return nil
}

func (c *Controller) requestReload() {
	c.reloadAccess.Lock()
	defer c.reloadAccess.Unlock()

	now := time.Now()
	if !c.lastReloadAt.IsZero() && now.Sub(c.lastReloadAt) < minReloadInterval {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"ago": now.Sub(c.lastReloadAt).String(),
		}).Warn("Reload requested too soon, suppressed")
		return
	}
	c.lastReloadAt = now

	if c.server.ReloadCh != nil {
		select {
		case c.server.ReloadCh <- struct{}{}:
		default:
		}
		return
	}
	log.WithField("tag", c.tag).Error("Reload requested but reload channel is not available")
}

func (c *Controller) applyRuntimeNodeInfo(node *panel.NodeInfo) {
	if node == nil {
		return
	}
	if c.nodeInfoMonitorPeriodic != nil {
		c.nodeInfoMonitorPeriodic.UpdateInterval(node.PullInterval)
	}
	if c.userReportPeriodic != nil {
		c.userReportPeriodic.UpdateInterval(node.PushInterval)
	}
	if c.heartbeatPeriodic != nil {
		c.heartbeatPeriodic.UpdateInterval(node.PushInterval)
	}
	c.infoAccess.Lock()
	c.info = node
	c.infoAccess.Unlock()
	c.apiClient.CommitNodeInfo(node)
}

func requiresCoreReload(oldNode, newNode *panel.NodeInfo) bool {
	if oldNode == nil || newNode == nil {
		return true
	}
	return !reflect.DeepEqual(coreReloadFingerprint(oldNode), coreReloadFingerprint(newNode))
}

func coreReloadReasons(oldNode, newNode *panel.NodeInfo) []string {
	if oldNode == nil || newNode == nil {
		return []string{"node"}
	}
	oldFp := coreReloadFingerprint(oldNode)
	newFp := coreReloadFingerprint(newNode)
	var reasons []string
	appendIfChanged := func(name string, oldValue, newValue interface{}) {
		if !reflect.DeepEqual(oldValue, newValue) {
			reasons = append(reasons, name)
		}
	}
	appendIfChanged("id", oldFp.Id, newFp.Id)
	appendIfChanged("type", oldFp.Type, newFp.Type)
	appendIfChanged("security", oldFp.Security, newFp.Security)
	appendIfChanged("tag", oldFp.Tag, newFp.Tag)
	appendIfChanged("protocol", oldFp.Common.Protocol, newFp.Common.Protocol)
	appendIfChanged("listen_ip", oldFp.Common.ListenIP, newFp.Common.ListenIP)
	appendIfChanged("server_port", oldFp.Common.ServerPort, newFp.Common.ServerPort)
	appendIfChanged("routes", oldFp.Common.Routes, newFp.Common.Routes)
	appendIfChanged("tls", oldFp.Common.Tls, newFp.Common.Tls)
	appendIfChanged("tls_settings", oldFp.Common.TlsSettings, newFp.Common.TlsSettings)
	appendIfChanged("up_mbps", oldFp.Common.UpMbps, newFp.Common.UpMbps)
	appendIfChanged("down_mbps", oldFp.Common.DownMbps, newFp.Common.DownMbps)
	appendIfChanged("obfs", oldFp.Common.Obfs, newFp.Common.Obfs)
	appendIfChanged("obfs_password", oldFp.Common.ObfsPassword, newFp.Common.ObfsPassword)
	appendIfChanged("ignore_client_bandwidth", oldFp.Common.IgnoreClientBandwidth, newFp.Common.IgnoreClientBandwidth)
	if len(reasons) == 0 {
		return []string{"unknown"}
	}
	return reasons
}

type nodeCoreFingerprint struct {
	Id       int
	Type     string
	Security int
	Tag      string
	Common   commonCoreFingerprint
}

type commonCoreFingerprint struct {
	Protocol              string
	ListenIP              string
	ServerPort            int
	Routes                []panel.Route
	Tls                   int
	TlsSettings           panel.TlsSettings
	UpMbps                int
	DownMbps              int
	Obfs                  string
	ObfsPassword          string
	IgnoreClientBandwidth bool
}

func coreReloadFingerprint(node *panel.NodeInfo) nodeCoreFingerprint {
	fingerprint := nodeCoreFingerprint{
		Id:       node.Id,
		Type:     node.Type,
		Security: node.Security,
		Tag:      node.Tag,
	}
	if node.Common == nil {
		return fingerprint
	}
	common := node.Common
	fingerprint.Common = commonCoreFingerprint{
		Protocol:              common.Protocol,
		ListenIP:              common.ListenIP,
		ServerPort:            common.ServerPort,
		Routes:                common.Routes,
		Tls:                   common.Tls,
		TlsSettings:           common.TlsSettings,
		UpMbps:                common.UpMbps,
		DownMbps:              common.DownMbps,
		Obfs:                  common.Obfs,
		ObfsPassword:          common.ObfsPassword,
		IgnoreClientBandwidth: common.Ignore_Client_Bandwidth,
	}
	return fingerprint
}
