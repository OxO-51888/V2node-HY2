package cmd

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/OxO-51888/V2node-HY2/conf"
	"github.com/OxO-51888/V2node-HY2/core"
	"github.com/OxO-51888/V2node-HY2/limiter"
	"github.com/OxO-51888/V2node-HY2/node"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	config string
	watch  bool
)

var serverCommand = cobra.Command{
	Use:   "server",
	Short: "Run v2node server",
	Run:   serverHandle,
	Args:  cobra.NoArgs,
}

func init() {
	serverCommand.PersistentFlags().
		StringVarP(&config, "config", "c",
			"/etc/v2node/config.json", "config file path")
	serverCommand.PersistentFlags().
		BoolVarP(&watch, "watch", "w",
			true, "watch file path change")
	command.AddCommand(&serverCommand)
}

func serverHandle(_ *cobra.Command, _ []string) {
	showVersion()
	c := conf.New()
	err := c.LoadFromPath(config)
	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
		DisableQuote:     true,
		PadLevelText:     false,
	})
	if err != nil {
		log.WithField("err", err).Error("Load config file failed")
		return
	}
	applyLogConfig(c)
	if c.PprofPort != 0 {
		go func() {
			log.Infof("Starting pprof server on :%d", c.PprofPort)
			if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", c.PprofPort), nil); err != nil {
				log.WithField("err", err).Error("pprof server failed")
			}
		}()
	}

	limiter.Init()
	nodes, err := node.New(c.NodeConfigs)
	if err != nil {
		log.WithField("err", err).Error("Get node info failed")
		return
	}
	log.Info("Got nodes info from server")

	reloadCh := make(chan struct{}, 1)
	v2core := core.New(c)
	v2core.ReloadCh = reloadCh
	if err := v2core.Start(nodes.NodeInfos); err != nil {
		log.WithField("err", err).Error("Start core failed")
		return
	}
	defer v2core.Close()

	if err := nodes.Start(c.NodeConfigs, v2core); err != nil {
		log.WithField("err", err).Error("Run nodes failed")
		return
	}
	log.Info("Nodes started")

	if watch {
		err = c.Watch(config, func() {
			select {
			case reloadCh <- struct{}{}:
			default:
			}
		})
		if err != nil {
			log.WithField("err", err).Error("start watch failed")
			return
		}
	}
	runtime.GC()

	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-osSignals:
			log.Info("received exit signal, shutting down")
			os.Exit(0)
		case <-reloadCh:
			log.Info("reload requested")
			if err := reload(config, &nodes, &v2core); err != nil {
				log.WithField("err", err).Error("reload failed")
				continue
			}
			log.Info("reload finished")
		}
	}
}

func reload(config string, nodes **node.Node, v2core **core.V2Core) error {
	if nodes == nil || *nodes == nil || v2core == nil || *v2core == nil {
		return fmt.Errorf("current runtime is empty")
	}

	oldNodes := *nodes
	oldCore := *v2core
	oldConf := oldCore.Config
	oldReloadCh := oldCore.ReloadCh

	newConf := conf.New()
	if err := newConf.LoadFromPath(config); err != nil {
		return err
	}
	newNodes, err := node.New(newConf.NodeConfigs)
	if err != nil {
		return err
	}
	newCore := core.New(newConf)
	newCore.ReloadCh = oldReloadCh
	if err := newCore.Start(newNodes.NodeInfos); err != nil {
		return err
	}

	if err := oldNodes.Close(); err != nil {
		return fmt.Errorf("close old nodes: %w", err)
	}
	if err := oldCore.Close(); err != nil {
		return fmt.Errorf("close old core: %w", err)
	}

	if err := newNodes.Start(newConf.NodeConfigs, newCore); err != nil {
		_ = newNodes.Close()
		_ = newCore.Close()
		if rollbackErr := rollbackRuntime(oldConf, oldReloadCh, nodes, v2core); rollbackErr != nil {
			return fmt.Errorf("start new nodes: %w; rollback failed: %v", err, rollbackErr)
		}
		log.WithField("err", err).Error("reload failed, old runtime restored")
		return nil
	}

	*nodes = newNodes
	*v2core = newCore
	applyLogConfig(newConf)
	runtime.GC()
	return nil
}

func rollbackRuntime(oldConf *conf.Conf, reloadCh chan struct{}, nodes **node.Node, v2core **core.V2Core) error {
	if oldConf == nil {
		return fmt.Errorf("old config is empty")
	}
	rollbackNodes, err := node.New(oldConf.NodeConfigs)
	if err != nil {
		return err
	}
	rollbackCore := core.New(oldConf)
	rollbackCore.ReloadCh = reloadCh
	if err := rollbackCore.Start(rollbackNodes.NodeInfos); err != nil {
		return err
	}
	if err := rollbackNodes.Start(oldConf.NodeConfigs, rollbackCore); err != nil {
		_ = rollbackNodes.Close()
		_ = rollbackCore.Close()
		return err
	}
	*nodes = rollbackNodes
	*v2core = rollbackCore
	return nil
}

func applyLogConfig(c *conf.Conf) {
	switch c.LogConfig.Level {
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warn", "warning":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	}
	if c.LogConfig.Output == "" {
		return
	}
	f, err := os.OpenFile(c.LogConfig.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.WithField("err", err).Error("open log file failed, using current output")
		return
	}
	if oldWriter, ok := log.StandardLogger().Out.(*os.File); ok && oldWriter != os.Stdout && oldWriter != os.Stderr {
		_ = oldWriter.Close()
	}
	log.SetOutput(f)
}
