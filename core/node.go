package core

import (
	"fmt"

	panel "github.com/OxO-51888/V2node-HY2/api/v2board"
)

func (v *V2Core) AddNode(tag string, info *panel.NodeInfo) error {
	if info == nil {
		return fmt.Errorf("node %s has empty config", tag)
	}
	if info.Type != "hysteria2" {
		return fmt.Errorf("HY2-only backend does not support node type: %s", info.Type)
	}
	return v.hy2.AddNode(tag, info)
}

func (v *V2Core) DelNode(tag string) error {
	if v.hy2.HasNode(tag) {
		return v.hy2.DelNode(tag)
	}
	return nil
}
