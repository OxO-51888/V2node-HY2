package limiter

import (
	"sync"
	"testing"
	"time"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
)

func TestLimiterConcurrentStateAndRevocation(t *testing.T) {
	Init()
	users := []panel.UserInfo{{Id: 1, Uuid: "user", DeviceLimit: 4}}
	alive := map[int]int{1: 0}
	l := AddLimiter("hysteria2", "test", users, alive)
	defer DeleteLimiter("test")
	alive[1] = 4
	if _, reject := l.CheckLimit("test|user", "192.0.2.1", true); reject {
		t.Fatal("caller mutated alive snapshot")
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				switch worker % 4 {
				case 0:
					l.CheckLimit("test|user", "192.0.2.1", true)
				case 1:
					l.GetOnlineDevice()
				case 2:
					m := map[int]int{1: i % 4}
					l.UpdateAliveList(m)
					m[1] = 100
				case 3:
					l.UpdateUser("test", nil, nil, users)
					_ = l.UpdateDynamicSpeedLimit("test", "user", 10, time.Now().Add(time.Minute))
				}
			}
		}(worker)
	}
	wg.Wait()
	old, _ := l.UserLimitInfo.Load("test|user")
	l.UpdateUser("test", nil, users, nil)
	if !old.(*UserLimitInfo).Revoked.Load() {
		t.Fatal("cached limit not revoked")
	}
	if _, reject := l.CheckLimit("test|user", "192.0.2.1", true); !reject {
		t.Fatal("deleted user allowed")
	}
	l.UpdateUser("test", users, nil, nil)
	l.UpdateAliveList(map[int]int{1: 0})
	if _, reject := l.CheckLimit("test|user", "192.0.2.1", true); reject {
		t.Fatal("re-added user denied")
	}
}

func TestExpiredDynamicLimitDoesNotRevokeUser(t *testing.T) {
	Init()
	l := AddLimiter("hysteria2", "expire", []panel.UserInfo{{Id: 1, Uuid: "user"}}, nil)
	defer DeleteLimiter("expire")
	if err := l.UpdateDynamicSpeedLimit("expire", "user", 10, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if bucket, reject := l.CheckLimit("expire|user", "192.0.2.1", true); reject || bucket != nil {
		t.Fatal("expired temporary speed limit should restore unrestricted user")
	}
}
