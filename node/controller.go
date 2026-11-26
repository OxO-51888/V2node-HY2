package node

import (
	"context"
	"fmt"
	"os"
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
	heartbeatPeriodic       *task.Task
	renewCertPeriodic       *task.Task
	reloadAccess            sync.Mutex
	syncUsersAccess         sync.Mutex
	userFetchAccess         sync.Mutex
	userGeneration          uint64
	onlineTrafficAccess     sync.RWMutex
	onlineSmallTraffic      map[int]bool
	closeAccess             sync.Mutex
	closed                  bool
	infoAccess              sync.RWMutex
	lastReloadAt            time.Time
	awaitingAdmission       bool
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
	return c.StartContext(context.Background(), x)
}

// The onboarding check reads these sparse lifecycle events from this systemd
// invocation only. Readiness must not depend on the configured log level.
func (c *Controller) admissionEvent(state string) {
	if os.Getenv("INVOCATION_ID") == "" {
		return
	}
	if info := c.currentInfo(); info != nil && info.Common != nil {
		fmt.Fprintf(os.Stderr, "v2node-admission port=%d state=%s\n", info.Common.ServerPort, state)
	}
}

func (c *Controller) StartContext(ctx context.Context, x *core.V2Core) (err error) {
	// Init Core
	c.server = x
	// First fetch Node Info
	node := c.info
	if node == nil {
		startCtx, cancel := panelRequestContext(ctx)
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
	// Bind once with no users. Accounting retries must not tear down the
	// listener or grant access using a pre-recovery user snapshot.
	if !c.awaitingAdmission {
		if err := ctx.Err(); err != nil {
			return err
		}
		if node.Security == panel.Tls {
			if err := c.requestCert(); err != nil {
				return fmt.Errorf("request cert: %w", err)
			}
		}
		c.limiter = limiter.AddLimiter(node.Type, node.Tag, nil, nil)
		if err := c.server.AddNode(node.Tag, node); err != nil {
			limiter.DeleteLimiter(node.Tag)
			c.limiter = nil
			return fmt.Errorf("add node: %w", err)
		}
		c.tag = node.Tag
		c.awaitingAdmission = true
		c.admissionEvent("waiting")
	}
	startCtx, cancel := panelRequestContext(ctx)
	err = c.apiClient.FlushTraffic(startCtx, func() ([]panel.UserTraffic, error) { return nil, nil },
		func([]panel.UserTraffic) error { return nil }, func([]int) error { return nil })
	cancel()
	if err != nil {
		return fmt.Errorf("recover traffic before accepting users: %w", err)
	}
	// Update user
	startCtx, cancel = panelRequestContext(ctx)
	snapshot, err := c.apiClient.GetUsers(startCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("get initial users: %w", err)
	} else if snapshot == nil {
		c.apiClient.InvalidateUsers()
		return fmt.Errorf("initial users response has no snapshot")
	}
	c.userList = snapshot.Users
	if len(c.userList) == 0 {
		log.WithField("tag", node.Tag).Warn("User list is empty, start node without users")
	}
	startCtx, cancel = panelRequestContext(ctx)
	c.aliveMap, err = c.apiClient.GetUserAlive(startCtx)
	cancel()
	if err != nil {
		log.WithFields(log.Fields{
			"tag": node.Tag,
			"err": err,
		}).Warn("Get alive list failed, continue with empty alive list")
		c.aliveMap = make(map[int]int)
	}
	if err = ctx.Err(); err != nil {
		return err
	}

	c.limiter.UpdateAliveList(c.aliveMap)
	c.limiter.UpdateUser(c.tag, c.userList, nil, nil)
	added, err := c.server.AddUsers(&core.AddUsersParams{
		Tag:      c.tag,
		Users:    c.userList,
		NodeInfo: node,
	})
	if err != nil {
		// A failed activation cannot leave a partially authorized listener.
		if delErr := c.server.DelNode(c.tag); delErr != nil {
			return fmt.Errorf("rollback admission: %w (activation: %v)", delErr, err)
		}
		limiter.DeleteLimiter(c.tag)
		c.awaitingAdmission = false
		c.tag = ""
		return fmt.Errorf("add users error: %s", err)
	}
	c.awaitingAdmission = false
	log.WithField("tag", c.tag).Infof("Added %d new users", added)
	c.info = node
	c.apiClient.CommitUsers(snapshot)
	c.apiClient.CommitNodeInfo(node)
	c.startTasks(node)
	c.admissionEvent("ready")
	return nil
}

func (c *Controller) currentInfo() *panel.NodeInfo {
	c.infoAccess.RLock()
	defer c.infoAccess.RUnlock()
	return c.info
}

// Close implement the Close() function of the service interface
func (c *Controller) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.CloseContext(ctx)
}

func (c *Controller) cancelTasks() {
	for _, t := range c.tasks() {
		if t != nil {
			t.Cancel()
		}
	}
}

func (c *Controller) tasks() []*task.Task {
	return []*task.Task{c.nodeInfoMonitorPeriodic, c.userReportPeriodic, c.heartbeatPeriodic, c.renewCertPeriodic}
}

func (c *Controller) CloseContext(ctx context.Context) error {
	c.closeAccess.Lock()
	defer c.closeAccess.Unlock()
	if c.closed {
		return nil
	}
	c.cancelTasks()
	for _, t := range c.tasks() {
		if t != nil {
			if err := t.CloseContext(ctx); err != nil {
				return err
			}
		}
	}
	if c.server == nil || c.tag == "" {
		return nil
	}
	c.admissionEvent("closing")
	if err := c.server.QuiesceNode(c.tag); err != nil {
		return err
	}
	if err := c.apiClient.PersistTraffic(ctx, func() ([]panel.UserTraffic, error) {
		return c.server.GetUserTrafficSlice(c.tag, 0)
	}); err != nil {
		return fmt.Errorf("persist final traffic: %w", err)
	}
	err := c.server.DelNode(c.tag)
	if err != nil {
		return fmt.Errorf("del node error: %s", err)
	}
	limiter.DeleteLimiter(c.tag)
	c.closed = true
	return nil
}
