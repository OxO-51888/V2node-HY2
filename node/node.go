package node

import (
	"context"
	"fmt"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/OxO-51888/V2node-HY2/core"
	log "github.com/sirupsen/logrus"
)

type Node struct {
	controllers []*Controller
	configs     []conf.NodeConfig
	stopCh      chan struct{}
	NodeInfos   []*panel.NodeInfo
}

func New(nodes []conf.NodeConfig) (*Node, error) {
	n := &Node{
		controllers: make([]*Controller, 0, len(nodes)),
		configs:     make([]conf.NodeConfig, 0, len(nodes)),
		stopCh:      make(chan struct{}),
		NodeInfos:   make([]*panel.NodeInfo, 0, len(nodes)),
	}
	for _, node := range nodes {
		nodeConfig := node
		p, err := panel.New(&nodeConfig)
		if err != nil {
			log.WithFields(log.Fields{
				"api": nodeConfig.APIHost,
				"id":  nodeConfig.NodeID,
				"err": err,
			}).Error("Create panel client failed, skip node")
			continue
		}
		ctx, cancel := panelRequestContext(context.Background())
		info, err := p.GetNodeInfo(ctx)
		cancel()
		if err != nil {
			log.WithFields(log.Fields{
				"api": nodeConfig.APIHost,
				"id":  nodeConfig.NodeID,
				"err": err,
			}).Error("Get node info failed, will retry after service starts")
		}
		n.controllers = append(n.controllers, NewController(p, &nodeConfig, info))
		n.configs = append(n.configs, nodeConfig)
		if info != nil {
			n.NodeInfos = append(n.NodeInfos, info)
		}
	}
	if len(n.controllers) == 0 {
		return nil, fmt.Errorf("no valid node controller")
	}
	return n, nil
}

func (n *Node) Start(nodes []conf.NodeConfig, core *core.V2Core) error {
	if len(n.configs) == 0 {
		n.configs = nodes
	}
	for i := range n.controllers {
		node := n.configs[i]
		controller := n.controllers[i]
		go startControllerWithRetry(n.stopCh, controller, node, core)
	}
	return nil
}

func startControllerWithRetry(stopCh <-chan struct{}, controller *Controller, node conf.NodeConfig, core *core.V2Core) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		err := controller.Start(core)
		if err == nil {
			select {
			case <-stopCh:
				_ = controller.Close()
				return
			default:
			}
			log.WithFields(log.Fields{
				"api": node.APIHost,
				"id":  node.NodeID,
			}).Info("Node controller started")
			return
		}
		log.WithFields(log.Fields{
			"api": node.APIHost,
			"id":  node.NodeID,
			"err": err,
		}).Error("Start node controller failed, retrying")
		select {
		case <-stopCh:
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func (n *Node) Close() error {
	var err error
	if n.stopCh != nil {
		close(n.stopCh)
		n.stopCh = nil
	}
	for _, c := range n.controllers {
		if err = c.Close(); err != nil {
			log.Errorf("close controller failed: %v", err)
			return err
		}
	}
	n.controllers = nil
	return nil
}
