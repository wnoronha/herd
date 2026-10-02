package roster

import (
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
	"herd/internal/identity"
)

// CustomDelegate allows external subsystems (such as KV store) to hook into memberlist delegate calls.
type CustomDelegate interface {
	NotifyMsg(b []byte)
	GetBroadcasts(overhead, limit int) [][]byte
	LocalState(join bool) []byte
	MergeRemoteState(buf []byte, join bool)
}

// KeyRegistrar allows registering remote peer public keys into the transport.
type KeyRegistrar interface {
	RegisterPeerKey(addrOrName string, pubKey identity.Key)
}

// Delegate implements memberlist.Delegate, memberlist.EventDelegate, and memberlist.PingDelegate.
type Delegate struct {
	store      *Store
	localMeta  *NodeMeta
	customDel  CustomDelegate
	keyReg     KeyRegistrar
	broadcasts [][]byte
	mu         sync.Mutex
}

// NewDelegate creates a new Delegate backed by the given Store.
func NewDelegate(store *Store, localMeta *NodeMeta) *Delegate {
	return &Delegate{
		store:     store,
		localMeta: localMeta,
	}
}

// SetKeyRegistrar sets the transport key registrar.
func (d *Delegate) SetKeyRegistrar(kr KeyRegistrar) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keyReg = kr
}

// SetCustomDelegate attaches a subsystem delegate (e.g. KV gossip delegate).
func (d *Delegate) SetCustomDelegate(cd CustomDelegate) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.customDel = cd
}

// NodeMeta implements memberlist.Delegate.
func (d *Delegate) NodeMeta(limit int) []byte {
	if d.localMeta == nil {
		return nil
	}
	data, err := d.localMeta.Encode()
	if err != nil {
		return nil
	}
	if len(data) > limit {
		return data[:limit]
	}
	return data
}

// NotifyMsg implements memberlist.Delegate.
func (d *Delegate) NotifyMsg(b []byte) {
	d.mu.Lock()
	cd := d.customDel
	d.mu.Unlock()
	if cd != nil {
		cd.NotifyMsg(b)
	}
}

// GetBroadcasts implements memberlist.Delegate.
func (d *Delegate) GetBroadcasts(overhead, limit int) [][]byte {
	d.mu.Lock()
	cd := d.customDel
	var res [][]byte
	if len(d.broadcasts) > 0 {
		res = append(res, d.broadcasts...)
		d.broadcasts = nil
	}
	d.mu.Unlock()

	if cd != nil {
		customBcasts := cd.GetBroadcasts(overhead, limit)
		if len(customBcasts) > 0 {
			res = append(res, customBcasts...)
		}
	}
	return res
}

// LocalState implements memberlist.Delegate (for TCP push/pull).
func (d *Delegate) LocalState(join bool) []byte {
	d.mu.Lock()
	cd := d.customDel
	d.mu.Unlock()
	if cd != nil {
		return cd.LocalState(join)
	}
	return nil
}

// MergeRemoteState implements memberlist.Delegate (for TCP push/pull).
func (d *Delegate) MergeRemoteState(buf []byte, join bool) {
	d.mu.Lock()
	cd := d.customDel
	d.mu.Unlock()
	if cd != nil {
		cd.MergeRemoteState(buf, join)
	}
}

// NotifyJoin implements memberlist.EventDelegate.
func (d *Delegate) NotifyJoin(node *memberlist.Node) {
	meta, _ := DecodeNodeMeta(node.Meta)
	d.store.UpsertMember(node.Name, node.Addr.String(), node.Port, "alive", meta)
	d.registerNodeKey(node, meta)
}

// NotifyLeave implements memberlist.EventDelegate.
func (d *Delegate) NotifyLeave(node *memberlist.Node) {
	d.store.SetMemberStatus(node.Name, "left")
}

// NotifyUpdate implements memberlist.EventDelegate.
func (d *Delegate) NotifyUpdate(node *memberlist.Node) {
	meta, _ := DecodeNodeMeta(node.Meta)
	d.store.UpsertMember(node.Name, node.Addr.String(), node.Port, "alive", meta)
	d.registerNodeKey(node, meta)
}

// TailcatRegistrar allows registering Tailcat addresses for nodes.
type TailcatRegistrar interface {
	RegisterPeerTailcat(addrOrName, tcAddr string)
}

func (d *Delegate) registerNodeKey(node *memberlist.Node, meta *NodeMeta) {
	d.mu.Lock()
	kr := d.keyReg
	d.mu.Unlock()
	if kr == nil || meta == nil || meta.TailcatAddr == "" {
		return
	}

	if tr, ok := kr.(TailcatRegistrar); ok {
		tr.RegisterPeerTailcat(node.Name, meta.TailcatAddr)
		if !node.Addr.IsLoopback() {
			tr.RegisterPeerTailcat(node.Address(), meta.TailcatAddr)
			tr.RegisterPeerTailcat(node.Addr.String(), meta.TailcatAddr)
		}
	}

	var pubKey identity.Key
	if err := pubKey.UnmarshalText([]byte(meta.TailcatAddr)); err == nil {
		kr.RegisterPeerKey(node.Address(), pubKey)
		kr.RegisterPeerKey(node.Name, pubKey)
		kr.RegisterPeerKey(node.Addr.String(), pubKey)
	}
}

// AckPayload implements memberlist.PingDelegate.
func (d *Delegate) AckPayload() []byte {
	return nil
}

// NotifyPingComplete implements memberlist.PingDelegate.
func (d *Delegate) NotifyPingComplete(other *memberlist.Node, rtt time.Duration, payload []byte) {
	d.store.UpdateRTT(other.Name, rtt)
}
