package node

import (
	"context"
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
	"github.com/OxO-51888/V2node-HY2/core"
)

func TestStartTasksEnablesPanelTimeoutExit(t *testing.T) {
	c := &Controller{
		server: &core.V2Core{ReloadCh: make(chan struct{}, 1)},
		info: &panel.NodeInfo{
			Tag:          "panel-test",
			PullInterval: time.Hour,
			PushInterval: time.Hour,
			Common:       &panel.CommonNode{},
		},
	}

	c.startTasks(c.info)
	defer c.nodeInfoMonitorPeriodic.Close()
	defer c.userReportPeriodic.Close()

	if c.nodeInfoMonitorPeriodic == nil || !c.nodeInfoMonitorPeriodic.ExitOnTimeout {
		t.Fatal("nodeInfoMonitor must exit on timeout for systemd restart")
	}
	if c.userReportPeriodic == nil || !c.userReportPeriodic.ExitOnTimeout {
		t.Fatal("reportUserTrafficTask must exit on timeout for systemd restart")
	}
	if c.nodeInfoMonitorPeriodic.Timeout != panelTaskTimeout {
		t.Fatalf("nodeInfoMonitor timeout = %s, want %s", c.nodeInfoMonitorPeriodic.Timeout, panelTaskTimeout)
	}
	if c.userReportPeriodic.Timeout != panelTaskTimeout {
		t.Fatalf("reportUserTrafficTask timeout = %s, want %s", c.userReportPeriodic.Timeout, panelTaskTimeout)
	}
}

func TestHasPanelTaskBudget(t *testing.T) {
	if !hasPanelTaskBudget(context.Background()) {
		t.Fatal("background context should have task budget")
	}

	enoughCtx, enoughCancel := context.WithDeadline(context.Background(), time.Now().Add(minPanelTaskBudget+time.Second))
	defer enoughCancel()
	if !hasPanelTaskBudget(enoughCtx) {
		t.Fatal("context with enough remaining time should have task budget")
	}

	lowCtx, lowCancel := context.WithDeadline(context.Background(), time.Now().Add(minPanelTaskBudget-time.Second))
	defer lowCancel()
	if hasPanelTaskBudget(lowCtx) {
		t.Fatal("context with low remaining time should not have task budget")
	}

	expiredCtx, expiredCancel := context.WithCancel(context.Background())
	expiredCancel()
	if hasPanelTaskBudget(expiredCtx) {
		t.Fatal("canceled context should not have task budget")
	}
}
