package kv

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func TestStoreCRUDAndPrefix(t *testing.T) {
	s := NewStore("node-1")

	// 1. Set and Get
	entry := s.Set("config/db/host", []byte("10.0.0.5"), 0)
	if entry.Key != "config/db/host" || string(entry.Value) != "10.0.0.5" {
		t.Fatalf("unexpected Set entry: %+v", entry)
	}

	got, ok := s.Get("config/db/host")
	if !ok || string(got.Value) != "10.0.0.5" {
		t.Fatalf("Get failed: got=%+v, ok=%v", got, ok)
	}

	// 2. Prefix List
	s.Set("config/db/port", []byte("5432"), 0)
	s.Set("config/redis/host", []byte("10.0.0.6"), 0)
	s.Set("other/key", []byte("val"), 0)

	list := s.List("config/db/")
	if len(list) != 2 {
		t.Errorf("expected 2 items for 'config/db/', got %d", len(list))
	}

	allConfig := s.List("config/")
	if len(allConfig) != 3 {
		t.Errorf("expected 3 items for 'config/', got %d", len(allConfig))
	}

	// 3. Delete
	deleted := s.Delete("config/db/port")
	if !deleted.Tombstone {
		t.Errorf("expected Tombstone true")
	}

	_, ok = s.Get("config/db/port")
	if ok {
		t.Errorf("expected key to be deleted")
	}
}

func TestLWWConflictResolution(t *testing.T) {
	s1 := NewStore("node-1")
	s2 := NewStore("node-2")

	now := time.Now()
	eOld := &Entry{
		Key:          "service/version",
		Value:        []byte("v1.0.0"),
		Timestamp:    now.Add(-5 * time.Second).UnixNano(),
		WriterNodeID: "node-1",
		Version:      1,
	}

	eNew := &Entry{
		Key:          "service/version",
		Value:        []byte("v2.0.0"),
		Timestamp:    now.UnixNano(),
		WriterNodeID: "node-2",
		Version:      2,
	}

	s1.MergeEntry(eOld)
	// Apply newer write
	if !s1.MergeEntry(eNew) {
		t.Errorf("expected newer entry to win")
	}

	got, _ := s1.Get("service/version")
	if string(got.Value) != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", string(got.Value))
	}

	// Try applying older write on s2, then s2's newer write
	s2.MergeEntry(eNew)
	if s2.MergeEntry(eOld) {
		t.Errorf("older entry should not overwrite newer entry")
	}

	got2, _ := s2.Get("service/version")
	if string(got2.Value) != "v2.0.0" {
		t.Errorf("expected v2.0.0 on s2, got %s", string(got2.Value))
	}
}

func TestTTLExpirationAndGC(t *testing.T) {
	s := NewStore("node-1")

	// Set with 50ms TTL
	s.Set("session/user123", []byte("token_xyz"), 50*time.Millisecond)

	got, ok := s.Get("session/user123")
	if !ok || string(got.Value) != "token_xyz" {
		t.Fatalf("expected key before expiration")
	}

	time.Sleep(70 * time.Millisecond)

	// Should be expired on Get
	_, ok = s.Get("session/user123")
	if ok {
		t.Errorf("expected key to be expired")
	}

	// Run GC
	removed := s.GC(10 * time.Millisecond)
	if removed < 1 {
		t.Errorf("expected GC to collect expired key, got %d", removed)
	}
}

func TestStoreWatch(t *testing.T) {
	s := NewStore("node-1")
	ch, cancel := s.Watch("events/")
	defer cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Set("events/alert", []byte("high_load"), 0)
		s.Set("other/ignore", []byte("noop"), 0)
	}()

	select {
	case entry := <-ch:
		if entry.Key != "events/alert" || !bytes.Equal(entry.Value, []byte("high_load")) {
			t.Errorf("unexpected watch event: %+v", entry)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("watch timed out")
	}
}

func TestStoreConcurrency(t *testing.T) {
	s := NewStore("node-concurrent")
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Set("counter", []byte("val"), 0)
				_, _ = s.Get("counter")
				_ = s.List("count")
			}
		}(i)
	}

	wg.Wait()
}
