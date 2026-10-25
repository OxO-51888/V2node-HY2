package officialhy2

import panel "github.com/OxO-51888/V2node-HY2/api/v2board"

// Revocation is set before this barrier. Callbacks that already passed the
// permission check finish counting; subsequent callbacks cannot add new bytes.
func (h *trafficLogger) settleUser(uuid string) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if value, ok := h.users.Load(uuid); ok {
		user := value.(*trafficUserCache)
		user.trafficMu.Lock()
		user.trafficMu.Unlock()
		h.users.CompareAndDelete(uuid, user)
	}
}

// Only acknowledged, quiescent counters may be discarded.
func (m *Manager) PruneRetiredUsers(tag string, uuids []string) []string {
	m.mu.RLock()
	n := m.nodes[tag]
	m.mu.RUnlock()
	if n == nil {
		return nil
	}
	n.auth.mu.Lock()
	defer n.auth.mu.Unlock()
	var removed []string
	for _, uuid := range uuids {
		if _, active := n.auth.users[uuid]; active {
			continue
		}
		if n.traffic.counter.GetUpCount(uuid) == 0 && n.traffic.counter.GetDownCount(uuid) == 0 {
			n.traffic.counter.Delete(uuid)
			removed = append(removed, uuid)
		}
	}
	return removed
}

// Stop forwarding but keep the accounting objects available for durable drain.
func (m *Manager) QuiesceNode(tag string) error {
	m.mu.RLock()
	n := m.nodes[tag]
	m.mu.RUnlock()
	if n == nil {
		return nil
	}
	n.auth.mu.RLock()
	users := make([]panel.UserInfo, 0, len(n.auth.users))
	for uuid, id := range n.auth.users {
		users = append(users, panel.UserInfo{Id: id, Uuid: uuid})
	}
	n.auth.mu.RUnlock()
	if err := m.DelUsers(tag, users); err != nil {
		return err
	}
	return n.stop()
}
