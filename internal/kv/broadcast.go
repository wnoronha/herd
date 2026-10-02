package kv

import (
	"encoding/json"

	"github.com/hashicorp/memberlist"
)

const (
	// BroadcastMagic distinguishes KV delta broadcast messages
	BroadcastMagic = byte(0x4B) // 'K'
)

// KVBroadcast implements memberlist.Broadcast for KV mutation entries.
type KVBroadcast struct {
	entry *Entry
	msg   []byte
}

// NewKVBroadcast creates a new KVBroadcast for the given entry.
func NewKVBroadcast(entry *Entry) *KVBroadcast {
	entryBytes, err := entry.Marshal()
	if err != nil {
		return nil
	}

	payload := make([]byte, 1+len(entryBytes))
	payload[0] = BroadcastMagic
	copy(payload[1:], entryBytes)

	return &KVBroadcast{
		entry: entry,
		msg:   payload,
	}
}

// Invalidates implements memberlist.Broadcast.
func (b *KVBroadcast) Invalidates(other memberlist.Broadcast) bool {
	otherKV, ok := other.(*KVBroadcast)
	if !ok {
		return false
	}
	if b.entry.Key == otherKV.entry.Key {
		return b.entry.WinsAgainst(otherKV.entry)
	}
	return false
}

// Message implements memberlist.Broadcast.
func (b *KVBroadcast) Message() []byte {
	return b.msg
}

// Finished implements memberlist.Broadcast.
func (b *KVBroadcast) Finished() {
}

// DecodeBroadcast decodes a raw broadcast payload if it matches BroadcastMagic.
func DecodeBroadcast(msg []byte) (*Entry, error) {
	if len(msg) < 2 || msg[0] != BroadcastMagic {
		return nil, nil
	}
	return UnmarshalEntry(msg[1:])
}

// EncodeState serializes a slice of entries for TCP push/pull anti-entropy.
func EncodeState(entries []*Entry) ([]byte, error) {
	return json.Marshal(entries)
}

// DecodeState deserializes an anti-entropy state payload.
func DecodeState(buf []byte) ([]*Entry, error) {
	if len(buf) == 0 {
		return nil, nil
	}
	var entries []*Entry
	if err := json.Unmarshal(buf, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
