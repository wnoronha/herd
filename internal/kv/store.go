package kv

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type watcher struct {
	id     uint64
	prefix string
	ch     chan *Entry
}

// Store is a thread-safe in-memory key-value store with Last-Write-Wins CRDT semantics.
type Store struct {
	mu          sync.RWMutex
	entries     map[string]*Entry
	watchers    map[uint64]*watcher
	nextWatchID atomic.Uint64
	nodeID      string
}

// NewStore creates a new in-memory KV Store.
func NewStore(nodeID string) *Store {
	return &Store{
		entries:  make(map[string]*Entry),
		watchers: make(map[uint64]*watcher),
		nodeID:   nodeID,
	}
}

// Get retrieves an unexpired, non-tombstone entry by key.
func (s *Store) Get(key string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[key]
	if !ok || entry.Tombstone || entry.IsExpired(time.Now()) {
		return nil, false
	}
	return entry.Clone(), true
}

// GetRaw retrieves an entry even if expired or tombstone (for internal sync checks).
func (s *Store) GetRaw(key string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	return entry.Clone(), true
}

// Set stores or updates a key with value and optional TTL.
func (s *Store) Set(key string, value []byte, ttl time.Duration) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var expiresAt int64 = 0
	if ttl > 0 {
		expiresAt = now.Add(ttl).UnixNano()
	}

	var newVersion uint64 = 1
	if existing, ok := s.entries[key]; ok {
		newVersion = existing.Version + 1
	}

	entry := &Entry{
		Key:          key,
		Value:        value,
		Timestamp:    now.UnixNano(),
		WriterNodeID: s.nodeID,
		Version:      newVersion,
		Tombstone:    false,
		ExpiresAt:    expiresAt,
	}

	s.entries[key] = entry
	s.notifyWatchers(entry)
	return entry.Clone()
}

// Delete marks a key with a tombstone according to LWW semantics.
func (s *Store) Delete(key string) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var newVersion uint64 = 1
	if existing, ok := s.entries[key]; ok {
		newVersion = existing.Version + 1
	}

	entry := &Entry{
		Key:          key,
		Value:        nil,
		Timestamp:    now.UnixNano(),
		WriterNodeID: s.nodeID,
		Version:      newVersion,
		Tombstone:    true,
		ExpiresAt:    0,
	}

	s.entries[key] = entry
	s.notifyWatchers(entry)
	return entry.Clone()
}

// List returns all active, unexpired keys matching the given prefix.
func (s *Store) List(prefix string) []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var results []*Entry
	for k, e := range s.entries {
		if strings.HasPrefix(k, prefix) && !e.Tombstone && !e.IsExpired(now) {
			results = append(results, e.Clone())
		}
	}
	return results
}

// MergeEntry applies an incoming remote entry using deterministic LWW conflict resolution.
// Returns true if the entry was applied (won), false if ignored.
func (s *Store) MergeEntry(incoming *Entry) bool {
	if incoming == nil || incoming.Key == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.entries[incoming.Key]
	if !exists || incoming.WinsAgainst(existing) {
		cloned := incoming.Clone()
		s.entries[incoming.Key] = cloned
		s.notifyWatchers(cloned)
		return true
	}

	return false
}

// ExportState exports all active and tombstone records for anti-entropy sync.
func (s *Store) ExportState() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		res = append(res, e.Clone())
	}
	return res
}

// ImportState reconciles a remote state slice and returns how many records were updated.
func (s *Store) ImportState(remote []*Entry) int {
	updated := 0
	for _, e := range remote {
		if s.MergeEntry(e) {
			updated++
		}
	}
	return updated
}

// GC cleans up expired entries and tombstones older than maxTombstoneAge.
func (s *Store) GC(maxTombstoneAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	tombstoneCutoff := now.Add(-maxTombstoneAge).UnixNano()
	removed := 0

	for k, e := range s.entries {
		if e.IsExpired(now) {
			delete(s.entries, k)
			removed++
		} else if e.Tombstone && e.Timestamp < tombstoneCutoff {
			delete(s.entries, k)
			removed++
		}
	}

	return removed
}

// Watch creates a subscription channel for real-time key updates.
func (s *Store) Watch(prefix string) (<-chan *Entry, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextWatchID.Add(1)
	ch := make(chan *Entry, 64)
	w := &watcher{
		id:     id,
		prefix: prefix,
		ch:     ch,
	}
	s.watchers[id] = w

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing, ok := s.watchers[id]; ok {
			delete(s.watchers, id)
			close(existing.ch)
		}
	}

	return ch, cancel
}

func (s *Store) notifyWatchers(entry *Entry) {
	for _, w := range s.watchers {
		if strings.HasPrefix(entry.Key, w.prefix) {
			select {
			case w.ch <- entry.Clone():
			default:
				// Dropped if watcher queue is full
			}
		}
	}
}
