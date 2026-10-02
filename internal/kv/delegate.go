package kv

import (
	"time"

	"github.com/hashicorp/memberlist"
)

// GossipDelegate bridges Store mutations and memberlist gossip / anti-entropy.
type GossipDelegate struct {
	store    *Store
	queue    *memberlist.TransmitLimitedQueue
	numNodes func() int
}

// NewGossipDelegate creates a new GossipDelegate for the given store.
func NewGossipDelegate(store *Store, numNodes func() int) *GossipDelegate {
	if numNodes == nil {
		numNodes = func() int { return 1 }
	}

	d := &GossipDelegate{
		store:    store,
		numNodes: numNodes,
	}

	d.queue = &memberlist.TransmitLimitedQueue{
		NumNodes:       numNodes,
		RetransmitMult: 3,
	}

	return d
}

// QueueBroadcast queues a mutation for UDP gossip broadcast.
func (d *GossipDelegate) QueueBroadcast(entry *Entry) {
	if entry == nil {
		return
	}
	b := NewKVBroadcast(entry)
	if b != nil {
		d.queue.QueueBroadcast(b)
	}
}

// SetAndBroadcast writes a key-value pair and immediately queues it for gossip dissemination.
func (d *GossipDelegate) SetAndBroadcast(key string, value []byte, ttl time.Duration) *Entry {
	entry := d.store.Set(key, value, ttl)
	d.QueueBroadcast(entry)
	return entry
}

// DeleteAndBroadcast writes a tombstone and immediately queues it for gossip dissemination.
func (d *GossipDelegate) DeleteAndBroadcast(key string) *Entry {
	entry := d.store.Delete(key)
	d.QueueBroadcast(entry)
	return entry
}

// NotifyMsg receives incoming UDP gossip user messages and applies them to the local store.
func (d *GossipDelegate) NotifyMsg(buf []byte) {
	entry, err := DecodeBroadcast(buf)
	if err == nil && entry != nil {
		d.store.MergeEntry(entry)
	}
}

// GetBroadcasts retrieves queued mutation deltas for memberlist.
func (d *GossipDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return d.queue.GetBroadcasts(overhead, limit)
}

// LocalState serializes the full local KV state for periodic TCP push/pull anti-entropy.
func (d *GossipDelegate) LocalState(join bool) []byte {
	entries := d.store.ExportState()
	data, err := EncodeState(entries)
	if err != nil {
		return nil
	}
	return data
}

// MergeRemoteState merges state received during a TCP push/pull anti-entropy exchange.
func (d *GossipDelegate) MergeRemoteState(buf []byte, join bool) {
	entries, err := DecodeState(buf)
	if err == nil && len(entries) > 0 {
		d.store.ImportState(entries)
	}
}
