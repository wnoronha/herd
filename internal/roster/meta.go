package roster

import (
	"encoding/json"
	"fmt"
	"time"
)

// NodeMeta contains metadata broadcast by a node across the gossip cluster.
type NodeMeta struct {
	NodeName    string            `json:"node_name"`
	TailcatAddr string            `json:"tailcat_addr"`
	Tags        map[string]string `json:"tags,omitempty"`
	Version     string            `json:"version,omitempty"`
	StartTime   time.Time         `json:"start_time"`
}

// Encode serializes the NodeMeta to JSON bytes.
func (m *NodeMeta) Encode() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

// DecodeNodeMeta deserializes JSON bytes into a NodeMeta struct.
func DecodeNodeMeta(data []byte) (*NodeMeta, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var meta NodeMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to decode node metadata: %w", err)
	}
	return &meta, nil
}
