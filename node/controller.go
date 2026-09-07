package node

import (
	"context"
	"fmt"
	"sync"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/common/task"
	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/OxO-51888/V2node-HY2/core"
	"github.com/OxO-51888/V2node-HY2/limiter"
	log "github.com/sirupsen/logrus"
)

type Controller struct {
	server                  *core.V2Core
	apiClient               *panel.Client
	tag                     string
	limiter                 *limiter.Limiter
	userList                []panel.UserInfo
	aliveMap                map[int]int
	conf                    *conf.NodeConfig
	info                    *panel.NodeInfo
	nodeInfoMonitorPeriodic *task.Task
	userReportPeriodic      *task.Task
	renewCertPeriodic       *task.Task
	reloadAccess            sync.Mutex
	syncUsersAccess         sync.Mutex
	lastReloadAt            time.Time
}

// NewController return a Node controller with default parameters.
func NewController(api *panel.Client, conf *conf.NodeConfig, info *panel.NodeInfo) *Controller {
	controller := &Controller{
		apiClient: api,
		info:      info,
		conf:      conf,
	}
	return controller
}

// Start implement the Start() function of the service interface
func (c *Controller) Start(x *core.V2Core) (err error) {
	// Init Core
	c.server = x
	var limiterAdded bool
	var nodeAdded bool
	defer func() {
		if err == nil {
			return
		}
		if nodeAdded && c.server != nil && c.tag != "" {
			if delErr := c.server.DelNode(c.tag); delErr != nil {
				log.WithFields(log.Fields{
					"tag": c.tag,
					"err": delErr,
				}).Warn("Cleanup node after failed start failed")
			}
		}
		if limiterAdded && c.tag != "" {
			limiter.DeleteLimiter(c.tag)
		}
	}()
	// First fetch Node Info
	node := c.info
	if node == nil {
		startCtx, cancel := panelRequestContext(context.Background())
		c.info, err = c.apiClient.GetNodeInfo(startCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("get node info error: %s", err)
		}
		node = c.info
	}
	if node == nil {
		return fmt.Errorf("node info is empty")
	}
	// Update user
	startCtx, cancel := panelRequestContext(context.Background())
	users, err := c.apiClient.GetUserList(startCtx)
	cancel()
	if err != nil {
		log.WithFields(log.Fields{
			"tag": node.Tag,
			"err": err,
		}).Warn("Get user list failed on start, start node with cached or empty users")
		if c.userList == nil {
			c.userList = []panel.UserInfo{}
		}
	} else if users != nil {
		c.userList = users
	}
	if len(c.userList) == 0 {
		log.WithField("tag", node.Tag).Warn("User list is empty, start node without users")
	}
	startCtx, cancel = panelRequestContext(context.Background())
	c.aliveMap, err = c.apiClient.GetUserAlive(startCtx)
	cancel()
	if err != nil {
		log.WithFields(log.Fields{
			"tag": node.Tag,
			"err": err,
		}).Warn("Get alive list failed, continue with empty alive list")
		c.aliveMap = make(map[int]int)
	}
	c.tag = node.Tag

	// add limiter
	l := limiter.AddLimiter(c.info.Type, c.tag, c.userList, c.aliveMap)
	c.limiter = l
	limiterAdded = true
	if node.Security == panel.Tls {
		err = c.requestCert()
		if err != nil {
			return fmt.Errorf("request cert error: %s", err)
		}
	}
	// Add new tag
	err = c.server.AddNode(c.tag, node)
	if err != nil {
		return fmt.Errorf("add new node error: %s", err)
	}
	nodeAdded = true
	added, err := c.server.AddUsers(&core.AddUsersParams{
		Tag:      c.tag,
		Users:    c.userList,
		NodeInfo: node,
	})
	if err != nil {
		return fmt.Errorf("add users error: %s", err)
	}
	log.WithField("tag", c.tag).Infof("Added %d new users", added)
	c.info = node
	c.startTasks(node)
	return nil
}

// Close implement the Close() function of the service interface
func (c *Controller) Close() error {
	if c.tag != "" {
		limiter.DeleteLimiter(c.tag)
	}
	if c.nodeInfoMonitorPeriodic != nil {
		c.nodeInfoMonitorPeriodic.Close()
	}
	if c.userReportPeriodic != nil {
		c.userReportPeriodic.Close()
	}
	if c.renewCertPeriodic != nil {
		c.renewCertPeriodic.Close()
	}
	if c.server == nil || c.tag == "" {
		return nil
	}
	err := c.server.DelNode(c.tag)
	if err != nil {
		return fmt.Errorf("del node error: %s", err)
	}
	return nil
}
