package kv

import (
	"encoding/json"
	"time"
)

// Entry represents a versioned, timestamped key-value record in the distributed store.
type Entry struct {
	Key          string `json:"key"`
	Value        []byte `json:"value,omitempty"`
	Timestamp    int64  `json:"timestamp"` // Unix nano timestamp
	WriterNodeID string `json:"writer_node_id"`
	Version      uint64 `json:"version"`
	Tombstone    bool   `json:"tombstone,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // Unix nano expiration (0 = never)
}

// IsExpired checks if the entry has passed its expiration time.
func (e *Entry) IsExpired(now time.Time) bool {
	if e.ExpiresAt > 0 && now.UnixNano() > e.ExpiresAt {
		return true
	}
	return false
}

// WinsAgainst returns true if entry e takes precedence over other using LWW rules.
func (e *Entry) WinsAgainst(other *Entry) bool {
	if other == nil {
		return true
	}
	if e.Timestamp != other.Timestamp {
		return e.Timestamp > other.Timestamp
	}
	if e.Version != other.Version {
		return e.Version > other.Version
	}
	// Tie-breaker: deterministic lexicographical order on WriterNodeID
	return e.WriterNodeID > other.WriterNodeID
}

// Clone creates a deep copy of the entry.
func (e *Entry) Clone() *Entry {
	if e == nil {
		return nil
	}
	var valCopy []byte
	if len(e.Value) > 0 {
		valCopy = make([]byte, len(e.Value))
		copy(valCopy, e.Value)
	}
	return &Entry{
		Key:          e.Key,
		Value:        valCopy,
		Timestamp:    e.Timestamp,
		WriterNodeID: e.WriterNodeID,
		Version:      e.Version,
		Tombstone:    e.Tombstone,
		ExpiresAt:    e.ExpiresAt,
	}
}

// Marshal serializes the Entry to JSON bytes.
func (e *Entry) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEntry parses JSON bytes into an Entry.
func UnmarshalEntry(data []byte) (*Entry, error) {
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}
