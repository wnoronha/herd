package kv

import (
	"bytes"
	"testing"
	"time"
)

func TestBroadcastSerializationAndInvalidation(t *testing.T) {
	now := time.Now().UnixNano()
	e1 := &Entry{
		Key:          "cluster/leader",
		Value:        []byte("node-1"),
		Timestamp:    now,
		WriterNodeID: "node-1",
		Version:      1,
	}
	e2 := &Entry{
		Key:          "cluster/leader",
		Value:        []byte("node-2"),
		Timestamp:    now + 1000,
		WriterNodeID: "node-2",
		Version:      2,
	}

	b1 := NewKVBroadcast(e1)
	b2 := NewKVBroadcast(e2)

	if b1 == nil || b2 == nil {
		t.Fatalf("failed to create broadcasts")
	}

	// Newer broadcast b2 should invalidate older broadcast b1
	if !b2.Invalidates(b1) {
		t.Errorf("expected b2 to invalidate b1")
	}
	if b1.Invalidates(b2) {
		t.Errorf("b1 should not invalidate newer b2")
	}

	// Decode broadcast message
	decoded, err := DecodeBroadcast(b1.Message())
	if err != nil {
		t.Fatalf("DecodeBroadcast error: %v", err)
	}
	if decoded.Key != "cluster/leader" || !bytes.Equal(decoded.Value, []byte("node-1")) {
		t.Errorf("decoded broadcast mismatch: %+v", decoded)
	}
}

func TestGossipDelegateBroadcastAndAntiEntropy(t *testing.T) {
	s1 := NewStore("node-1")
	s2 := NewStore("node-2")

	d1 := NewGossipDelegate(s1, func() int { return 2 })
	d2 := NewGossipDelegate(s2, func() int { return 2 })

	// 1. Set and queue broadcast on d1
	entry := s1.Set("app/config/theme", []byte("dark"), 0)
	d1.QueueBroadcast(entry)

	// Retrieve broadcast buffers
	broadcasts := d1.GetBroadcasts(10, 1024)
	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast from d1, got %d", len(broadcasts))
	}

	// 2. Feed broadcast to d2 via NotifyMsg
	d2.NotifyMsg(broadcasts[0])

	got, ok := s2.Get("app/config/theme")
	if !ok || string(got.Value) != "dark" {
		t.Fatalf("d2 failed to apply broadcast: got=%+v, ok=%v", got, ok)
	}

	// 3. Test Full State Anti-Entropy (LocalState -> MergeRemoteState)
	s1.Set("sync/key1", []byte("val1"), 0)
	s1.Set("sync/key2", []byte("val2"), 0)

	stateBuf := d1.LocalState(false)
	if len(stateBuf) == 0 {
		t.Fatalf("expected non-empty LocalState buffer")
	}

	d2.MergeRemoteState(stateBuf, false)

	k1, ok1 := s2.Get("sync/key1")
	k2, ok2 := s2.Get("sync/key2")
	if !ok1 || string(k1.Value) != "val1" {
		t.Errorf("sync/key1 missing or mismatch on s2")
	}
	if !ok2 || string(k2.Value) != "val2" {
		t.Errorf("sync/key2 missing or mismatch on s2")
	}
}
