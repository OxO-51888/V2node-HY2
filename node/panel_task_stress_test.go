package node

import (
	"os"
	"strconv"
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
)

func TestPanelTaskStress(t *testing.T) {
	if os.Getenv("V2NODE_STRESS") != "1" {
		t.Skip("set V2NODE_STRESS=1 to run the panel task stress test")
	}

	rounds := stressEnvInt("V2NODE_STRESS_ROUNDS", 10)
	attempts := stressEnvInt("V2NODE_STRESS_ATTEMPTS", 1000000)
	sites := []string{"gm", "nnm", "ovo", "yiyuan", "clash", "pianyi"}
	base := &panel.NodeInfo{
		Id:           1,
		Type:         "hysteria2",
		Security:     panel.Tls,
		PushInterval: time.Minute,
		PullInterval: time.Minute,
		Tag:          "stress",
		Common: &panel.CommonNode{
			Protocol:   "hysteria2",
			ListenIP:   "0.0.0.0",
			ServerPort: 443,
			BaseConfig: &panel.BaseConfig{},
			Tls:        panel.Tls,
			TlsSettings: panel.TlsSettings{
				ServerName: "stress.example.com",
				CertMode:   "file",
			},
			UpMbps:   100,
			DownMbps: 100,
		},
	}

	for _, site := range sites {
		t.Run(site, func(t *testing.T) {
			siteBase := *base
			siteBase.Tag = site
			start := time.Now()
			total := 0
			for round := 0; round < rounds; round++ {
				intervalOnly := siteBase
				intervalOnly.PullInterval = siteBase.PullInterval + time.Duration(round+1)*time.Second
				intervalOnly.PushInterval = siteBase.PushInterval + time.Duration(round+1)*time.Second
				if requiresCoreReload(&siteBase, &intervalOnly) {
					t.Fatal("interval-only node changes must not require a core reload")
				}

				coreChanged := siteBase
				commonChanged := *siteBase.Common
				coreChanged.Common = &commonChanged
				coreChanged.Common.ServerPort = siteBase.Common.ServerPort + round + 1
				if !requiresCoreReload(&siteBase, &coreChanged) {
					t.Fatal("core node changes must require a core reload")
				}

				for i := 0; i < attempts; i++ {
					if !hasPanelTaskBudget(nilDeadlineContext{}) {
						t.Fatal("context without a deadline should have task budget")
					}
					if requiresCoreReload(&siteBase, &intervalOnly) {
						t.Fatal("interval-only node changes must stay non-core under stress")
					}
					if !requiresCoreReload(&siteBase, &coreChanged) {
						t.Fatal("core node changes must stay core under stress")
					}
					total++
				}
				t.Logf("%s round %d/%d passed, attempts=%d", site, round+1, rounds, attempts)
			}
			t.Logf("%s panel task stress passed: rounds=%d attempts_per_round=%d total=%d elapsed=%s", site, rounds, attempts, total, time.Since(start))
		})
	}
}

type nilDeadlineContext struct{}

func (nilDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (nilDeadlineContext) Done() <-chan struct{}       { return nil }
func (nilDeadlineContext) Err() error                  { return nil }
func (nilDeadlineContext) Value(any) any               { return nil }

func stressEnvInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
