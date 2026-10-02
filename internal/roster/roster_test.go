package roster

import (
	"net"
	"testing"
	"time"

	"github.com/hashicorp/memberlist"
)

func TestNodeMetaEncodeDecode(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	meta := &NodeMeta{
		NodeName:    "alpha",
		TailcatAddr: "pubkey-tailcat-addr",
		Tags:        map[string]string{"env": "test", "zone": "us-west"},
		Version:     "1.0.0",
		StartTime:   now,
	}

	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("meta.Encode() error: %v", err)
	}

	decoded, err := DecodeNodeMeta(encoded)
	if err != nil {
		t.Fatalf("DecodeNodeMeta() error: %v", err)
	}

	if decoded.NodeName != meta.NodeName {
		t.Errorf("expected node name %s, got %s", meta.NodeName, decoded.NodeName)
	}
	if decoded.TailcatAddr != meta.TailcatAddr {
		t.Errorf("expected tailcat addr %s, got %s", meta.TailcatAddr, decoded.TailcatAddr)
	}
	if decoded.Tags["env"] != "test" || decoded.Tags["zone"] != "us-west" {
		t.Errorf("tags mismatch: %+v", decoded.Tags)
	}
}

func TestRosterStoreAndDelegates(t *testing.T) {
	localMeta := &NodeMeta{
		NodeName:    "local-node",
		TailcatAddr: "local-tailcat-key",
		Tags:        map[string]string{"role": "leader"},
		Version:     "1.0.0",
		StartTime:   time.Now(),
	}

	store := NewStore(localMeta)
	delegate := NewDelegate(store, localMeta)

	// Check local node in store
	members := store.GetMembers()
	if len(members) != 1 || members[0].Name != "local-node" {
		t.Fatalf("expected 1 local member, got %d", len(members))
	}

	// 1. Test NodeMeta limit
	metaBytes := delegate.NodeMeta(512)
	if len(metaBytes) == 0 {
		t.Errorf("expected non-empty NodeMeta bytes")
	}

	// 2. Test NotifyJoin
	remoteMeta := &NodeMeta{
		NodeName:    "peer-node",
		TailcatAddr: "peer-tailcat-key",
		Version:     "1.0.0",
	}
	remoteMetaBytes, _ := remoteMeta.Encode()

	remoteNode := &memberlist.Node{
		Name: "peer-node",
		Addr: net.ParseIP("192.168.1.50"),
		Port: 7946,
		Meta: remoteMetaBytes,
	}

	delegate.NotifyJoin(remoteNode)

	member, ok := store.GetMember("peer-node")
	if !ok {
		t.Fatalf("peer-node not found in store after NotifyJoin")
	}
	if member.Status != "alive" || member.Addr != "192.168.1.50" || member.Port != 7946 {
		t.Errorf("unexpected member state: %+v", member)
	}
	if member.Meta == nil || member.Meta.TailcatAddr != "peer-tailcat-key" {
		t.Errorf("peer meta missing or invalid: %+v", member.Meta)
	}

	// 3. Test NotifyPingComplete
	delegate.NotifyPingComplete(remoteNode, 15*time.Millisecond, nil)
	member, _ = store.GetMember("peer-node")
	if member.LastRTT != 15*time.Millisecond {
		t.Errorf("expected LastRTT 15ms, got %v", member.LastRTT)
	}

	// 4. Test NotifyLeave
	delegate.NotifyLeave(remoteNode)
	member, _ = store.GetMember("peer-node")
	if member.Status != "left" {
		t.Errorf("expected member status left, got %s", member.Status)
	}
}
