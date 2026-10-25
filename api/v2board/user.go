package panel

import (
	"context"
	"encoding/json/v2"
	"fmt"
)

type OnlineUser struct {
	UID int
	IP  string
}
type UserInfo struct {
	Id          int    `json:"id"`
	Uuid        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`
	DeviceLimit int    `json:"device_limit"`
}
type UserSnapshot struct {
	Users []UserInfo `json:"users"`
	ETag  string     `json:"-"`
}
type UserTraffic struct {
	UID              int
	Upload, Download int64
	UUID             string `json:"-"`
}

func (c *Client) GetUsers(ctx context.Context) (*UserSnapshot, error) {
	c.cacheMu.Lock()
	etag := c.userEtag
	c.cacheMu.Unlock()
	body, next, unchanged, err := c.request(ctx, "GET", "users", etag, nil)
	if err != nil || unchanged {
		return nil, err
	}
	var snapshot UserSnapshot
	if err = json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("decode users: %w", err)
	}
	if snapshot.Users == nil {
		return nil, fmt.Errorf("missing or null users array")
	}
	ids := make(map[int]bool, len(snapshot.Users))
	uuids := make(map[string]bool, len(snapshot.Users))
	for _, u := range snapshot.Users {
		if u.Id <= 0 || u.Uuid == "" || u.SpeedLimit < 0 || u.DeviceLimit < 0 || ids[u.Id] || uuids[u.Uuid] {
			return nil, fmt.Errorf("invalid or duplicate user")
		}
		ids[u.Id] = true
		uuids[u.Uuid] = true
	}
	snapshot.ETag = next
	return &snapshot, nil
}

func (c *Client) CommitUsers(snapshot *UserSnapshot) {
	if snapshot == nil {
		return
	}
	c.cacheMu.Lock()
	c.userEtag = snapshot.ETag
	c.cacheMu.Unlock()
}

func (c *Client) InvalidateUsers() {
	c.cacheMu.Lock()
	c.userEtag = ""
	c.cacheMu.Unlock()
}

func (c *Client) GetUserAlive(ctx context.Context) (map[int]int, error) {
	body, _, _, err := c.request(ctx, "GET", "alive", "", nil)
	if err != nil {
		return nil, err
	}
	var data struct {
		Alive map[int]int `json:"alive"`
	}
	if err = json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode alive: %w", err)
	}
	if data.Alive == nil {
		return nil, fmt.Errorf("missing alive object")
	}
	for uid, n := range data.Alive {
		if uid <= 0 || n < 0 {
			return nil, fmt.Errorf("invalid alive count")
		}
	}
	return data.Alive, nil
}

func (c *Client) ReportNodeOnlineUsers(ctx context.Context, data *map[int][]string) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	body, _, _, err = c.request(ctx, "POST", "alive", "", body)
	if err != nil {
		return err
	}
	var ack struct {
		Accepted bool `json:"accepted"`
	}
	if json.Unmarshal(body, &ack) != nil || !ack.Accepted {
		return fmt.Errorf("online report was not acknowledged")
	}
	return nil
}
