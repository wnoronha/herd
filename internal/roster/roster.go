package roster

import (
	"sync"
	"time"
)

// Member represents a cluster node with its active state and metadata.
type Member struct {
	Name     string            `json:"name"`
	Addr     string            `json:"addr"`
	Port     uint16            `json:"port"`
	Status   string            `json:"status"` // alive, suspect, dead, left
	Meta     *NodeMeta         `json:"meta,omitempty"`
	LastRTT  time.Duration     `json:"last_rtt,omitempty"`
	LastSeen time.Time         `json:"last_seen"`
}

// Store maintains a thread-safe in-memory registry of cluster members.
type Store struct {
	mu      sync.RWMutex
	members map[string]*Member
	local   *NodeMeta
}

// NewStore creates a new roster Store with the local node metadata.
func NewStore(localMeta *NodeMeta) *Store {
	s := &Store{
		members: make(map[string]*Member),
		local:   localMeta,
	}
	if localMeta != nil {
		s.members[localMeta.NodeName] = &Member{
			Name:     localMeta.NodeName,
			Status:   "alive",
			Meta:     localMeta,
			LastSeen: time.Now(),
		}
	}
	return s
}

// GetMembers returns a snapshot slice of all known members.
func (s *Store) GetMembers() []Member {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Member, 0, len(s.members))
	for _, m := range s.members {
		result = append(result, *m)
	}
	return result
}

// GetMember retrieves a single member by name.
func (s *Store) GetMember(name string) (Member, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.members[name]
	if !ok {
		return Member{}, false
	}
	return *m, true
}

// UpsertMember updates or inserts member details.
func (s *Store) UpsertMember(name string, addr string, port uint16, status string, meta *NodeMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.members[name]
	if !ok {
		s.members[name] = &Member{
			Name:     name,
			Addr:     addr,
			Port:     port,
			Status:   status,
			Meta:     meta,
			LastSeen: time.Now(),
		}
		return
	}

	existing.Addr = addr
	existing.Port = port
	existing.Status = status
	if meta != nil {
		existing.Meta = meta
	}
	existing.LastSeen = time.Now()
}

// SetMemberStatus updates the lifecycle status of a member.
func (s *Store) SetMemberStatus(name string, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.members[name]; ok {
		m.Status = status
		m.LastSeen = time.Now()
	}
}

// UpdateRTT updates the ping round-trip time for a member.
func (s *Store) UpdateRTT(name string, rtt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.members[name]; ok {
		m.LastRTT = rtt
		m.LastSeen = time.Now()
	}
}
