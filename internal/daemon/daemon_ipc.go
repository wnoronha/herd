package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"herd/internal/agent"
	"herd/internal/ipc"
	"herd/internal/mailbox"
	"herd/internal/transport/tailcat"
)

// StopResponse is an alias for ipc.StopResponse.
type StopResponse = ipc.StopResponse

// GetStatus implements ipc.DaemonHandler.
func (d *Daemon) GetStatus(ctx context.Context) (*ipc.StatusResponse, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	memberCount := 1
	if d.Memberlist != nil {
		memberCount = d.Memberlist.NumMembers()
	}

	bindPort := d.Config.BindPort
	if d.Transport != nil {
		bindPort = d.Transport.GetPort()
	}

	tcAddr := d.Identity.TailcatAddr()
	if d.Transport != nil && d.Transport.TailcatAddr() != "" {
		tcAddr = d.Transport.TailcatAddr()
	}

	return &ipc.StatusResponse{
		NodeName:      d.Config.NodeName,
		PublicKey:     d.Identity.PublicKey.String(),
		TailcatAddr:   tcAddr,
		BindPort:      bindPort,
		State:         d.state,
		MemberCount:   memberCount,
		UptimeSeconds: int64(time.Since(d.StartTime).Seconds()),
	}, nil
}

// GetRoster implements ipc.DaemonHandler.
func (d *Daemon) GetRoster(ctx context.Context) (*ipc.RosterResponse, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	members := d.Store.GetMembers()
	ipcMembers := make([]ipc.MemberInfo, 0, len(members))

	for _, m := range members {
		var metaMap map[string]string
		if m.Meta != nil {
			metaMap = m.Meta.Tags
		}
		ipcMembers = append(ipcMembers, ipc.MemberInfo{
			Name:     m.Name,
			Addr:     m.Addr,
			Port:     m.Port,
			Status:   m.Status,
			Metadata: metaMap,
		})
	}

	return &ipc.RosterResponse{
		Members: ipcMembers,
	}, nil
}

// JoinNode implements ipc.DaemonHandler.
func (d *Daemon) JoinNode(ctx context.Context, addr string) (*ipc.JoinResponse, error) {
	if addr == "" {
		return nil, errors.New("join address cannot be empty")
	}

	d.mu.RLock()
	ml := d.Memberlist
	d.mu.RUnlock()

	if ml == nil {
		return nil, errors.New("memberlist is not running")
	}

	if strings.HasPrefix(addr, "tcp") {
		// Bootstrap join over Tailcat DERP stream
		secConn, err := d.Transport.DialStream(addr, tailcat.StreamTypeGossip, 15*time.Second)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to Tailcat peer %s: %w", addr, err)
		}
		remoteAddrStr := secConn.RemoteAddr().String()
		_ = secConn.Close()

		d.Transport.RegisterPeerTailcat(addr, addr)
		d.Transport.RegisterPeerTailcat(remoteAddrStr, addr)
		if host, _, err := net.SplitHostPort(remoteAddrStr); err == nil && host != "" {
			d.Transport.RegisterPeerTailcat(host, addr)
		}

		num, err := ml.Join([]string{remoteAddrStr})
		if err != nil {
			return nil, fmt.Errorf("failed to join memberlist overlay at %s: %w", remoteAddrStr, err)
		}

		return &ipc.JoinResponse{
			JoinedNodes: num,
			Message:     fmt.Sprintf("successfully joined cluster over Tailcat DERP (%d node)", num),
		}, nil
	}

	num, err := ml.Join([]string{addr})
	if err != nil {
		return nil, fmt.Errorf("failed to join peer at %s: %w", addr, err)
	}

	return &ipc.JoinResponse{
		JoinedNodes: num,
		Message:     fmt.Sprintf("successfully contacted %d node(s)", num),
	}, nil
}

// LeaveCluster implements ipc.DaemonHandler.
func (d *Daemon) LeaveCluster(ctx context.Context) (*ipc.LeaveResponse, error) {
	d.mu.RLock()
	ml := d.Memberlist
	d.mu.RUnlock()

	if ml != nil {
		_ = ml.Leave(1 * time.Second)
	}

	return &ipc.LeaveResponse{
		Message: "gracefully departed gossip cluster",
	}, nil
}

// StopDaemon implements ipc.DaemonHandler.
func (d *Daemon) StopDaemon(ctx context.Context) (*StopResponse, error) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = d.Stop()
	}()

	return &ipc.StopResponse{
		Message: "daemon shutdown initiated",
	}, nil
}

// KVGet implements ipc.DaemonHandler.
func (d *Daemon) KVGet(ctx context.Context, req *ipc.KVGetRequest) (*ipc.KVGetResponse, error) {
	if req == nil || req.Key == "" {
		return nil, errors.New("key cannot be empty")
	}

	d.mu.RLock()
	store := d.KVStore
	d.mu.RUnlock()

	if store == nil {
		return nil, errors.New("kv store not initialized")
	}

	entry, ok := store.Get(req.Key)
	if !ok || entry == nil {
		return &ipc.KVGetResponse{Found: false}, nil
	}

	return &ipc.KVGetResponse{
		Found: true,
		Entry: &ipc.KVEntryDTO{
			Key:          entry.Key,
			Value:        string(entry.Value),
			Version:      entry.Version,
			Timestamp:    entry.Timestamp,
			WriterNodeID: entry.WriterNodeID,
			Tombstone:    entry.Tombstone,
		},
	}, nil
}

// KVSet implements ipc.DaemonHandler.
func (d *Daemon) KVSet(ctx context.Context, req *ipc.KVSetRequest) (*ipc.KVSetResponse, error) {
	if req == nil || req.Key == "" {
		return nil, errors.New("key cannot be empty")
	}

	d.mu.RLock()
	delegate := d.KVDelegate
	d.mu.RUnlock()

	if delegate == nil {
		return nil, errors.New("kv delegate not initialized")
	}

	var ttl time.Duration
	if req.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(req.TTL)
		if err != nil {
			return nil, fmt.Errorf("invalid ttl duration %q: %w", req.TTL, err)
		}
	}

	entry := delegate.SetAndBroadcast(req.Key, []byte(req.Value), ttl)
	return &ipc.KVSetResponse{
		Key:     entry.Key,
		Version: entry.Version,
		Message: "ok",
	}, nil
}

// KVDelete implements ipc.DaemonHandler.
func (d *Daemon) KVDelete(ctx context.Context, req *ipc.KVDeleteRequest) (*ipc.KVDeleteResponse, error) {
	if req == nil || req.Key == "" {
		return nil, errors.New("key cannot be empty")
	}

	d.mu.RLock()
	delegate := d.KVDelegate
	d.mu.RUnlock()

	if delegate == nil {
		return nil, errors.New("kv delegate not initialized")
	}

	delegate.DeleteAndBroadcast(req.Key)
	return &ipc.KVDeleteResponse{
		Key:     req.Key,
		Message: "deleted",
	}, nil
}

// KVList implements ipc.DaemonHandler.
func (d *Daemon) KVList(ctx context.Context, req *ipc.KVListRequest) (*ipc.KVListResponse, error) {
	d.mu.RLock()
	store := d.KVStore
	d.mu.RUnlock()

	if store == nil {
		return nil, errors.New("kv store not initialized")
	}

	prefix := ""
	if req != nil {
		prefix = req.Prefix
	}

	entries := store.List(prefix)
	dtos := make([]ipc.KVEntryDTO, 0, len(entries))
	for _, e := range entries {
		dtos = append(dtos, ipc.KVEntryDTO{
			Key:          e.Key,
			Value:        string(e.Value),
			Version:      e.Version,
			Timestamp:    e.Timestamp,
			WriterNodeID: e.WriterNodeID,
			Tombstone:    e.Tombstone,
		})
	}

	return &ipc.KVListResponse{
		Entries: dtos,
	}, nil
}

// AgentPrompt implements ipc.DaemonHandler.
func (d *Daemon) AgentPrompt(ctx context.Context, req *ipc.AgentPromptRequest) (*ipc.AgentPromptResponse, error) {
	if req == nil || req.Prompt == "" {
		return nil, errors.New("prompt cannot be empty")
	}

	d.mu.RLock()
	engine := d.AgentEngine
	d.mu.RUnlock()

	if engine == nil {
		return nil, errors.New("agent engine is not initialized")
	}

	pReq := &agent.PromptRequest{
		Prompt:     req.Prompt,
		TargetNode: req.TargetNode,
		Role:       req.Role,
		Model:      req.Model,
		Provider:   req.Provider,
	}

	d.Logger.Named("agent").Infof("Received task prompt: %q", req.Prompt)

	resp, err := engine.ExecutePrompt(ctx, pReq)
	if err != nil {
		d.Logger.Named("agent").Errorf("Execution failed: %v", err)
		return nil, err
	}

	d.Logger.Named("agent").Infof("Prompt completed in %dms (model: %s, tools called: %d)",
		resp.ExecutionMs, resp.ModelUsed, len(resp.ToolsCalled))

	var toolsDTO []ipc.ToolCallDTO
	for _, tc := range resp.ToolsCalled {
		toolsDTO = append(toolsDTO, ipc.ToolCallDTO{
			Name:      tc.Name,
			Arguments: tc.Arguments,
			Result:    tc.Result,
		})
	}

	nodeName := resp.NodeName
	if nodeName == "" {
		nodeName = d.Config.NodeName
	}
	agentRole := resp.AgentRole
	if agentRole == "" {
		if req.Role != "" {
			agentRole = req.Role
		} else {
			agentRole = "coordinator"
		}
	}

	return &ipc.AgentPromptResponse{
		Response:    resp.Response,
		ToolsCalled: toolsDTO,
		ModelUsed:   resp.ModelUsed,
		ExecutionMs: resp.ExecutionMs,
		NodeName:    nodeName,
		AgentRole:   agentRole,
	}, nil
}

// MailSend implements ipc.DaemonHandler.
func (d *Daemon) MailSend(ctx context.Context, req *ipc.MailSendRequest) (*ipc.MailSendResponse, error) {
	if req == nil || req.To == "" {
		return nil, errors.New("destination 'to' cannot be empty")
	}
	if req.Message == "" {
		return nil, errors.New("message cannot be empty")
	}

	d.mu.RLock()
	delegate := d.KVDelegate
	store := d.KVStore
	fromNode := d.Config.NodeName
	d.mu.RUnlock()

	if store == nil && delegate == nil {
		return nil, errors.New("kv store not initialized")
	}

	topic := req.Topic
	if topic == "" {
		topic = "general"
	}

	msg := mailbox.NewMessage(fromNode, req.To, topic, req.Message)
	if req.ThreadID != "" {
		msg.ThreadID = req.ThreadID
	}

	ttl := 1 * time.Hour
	if req.TTL != "" {
		parsedTTL, err := time.ParseDuration(req.TTL)
		if err != nil {
			return nil, fmt.Errorf("invalid ttl duration %q: %w", req.TTL, err)
		}
		ttl = parsedTTL
	}

	valBytes, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize mailbox message: %w", err)
	}

	key := mailbox.KeyFor(req.To, msg.ID)
	if delegate != nil {
		delegate.SetAndBroadcast(key, valBytes, ttl)
	} else {
		store.Set(key, valBytes, ttl)
	}

	return &ipc.MailSendResponse{
		ID:     msg.ID,
		Key:    key,
		Status: "queued",
	}, nil
}

// MailList implements ipc.DaemonHandler.
func (d *Daemon) MailList(ctx context.Context, req *ipc.MailListRequest) (*ipc.MailListResponse, error) {
	d.mu.RLock()
	store := d.KVStore
	selfNode := d.Config.NodeName
	d.mu.RUnlock()

	if store == nil {
		return nil, errors.New("kv store not initialized")
	}

	target := selfNode
	var topicFilter string
	if req != nil {
		if req.Target != "" {
			target = mailbox.NormalizeAddress(req.Target)
		}
		topicFilter = req.Topic
	}

	// Read from canonical mailbox prefix
	entries := store.List(mailbox.PrefixFor(target))

	var messages []mailbox.Message
	for _, entry := range entries {
		var msg mailbox.Message
		if err := json.Unmarshal(entry.Value, &msg); err == nil {
			if topicFilter != "" && !strings.EqualFold(msg.Topic, topicFilter) {
				continue
			}
			messages = append(messages, msg)
		}
	}

	return &ipc.MailListResponse{
		Messages: messages,
	}, nil
}

// MailRead implements ipc.DaemonHandler.
func (d *Daemon) MailRead(ctx context.Context, req *ipc.MailReadRequest) (*ipc.MailReadResponse, error) {
	d.mu.RLock()
	store := d.KVStore
	delegate := d.KVDelegate
	selfNode := d.Config.NodeName
	d.mu.RUnlock()

	if store == nil {
		return nil, errors.New("kv store not initialized")
	}

	target := selfNode
	var topicFilter string
	var ack bool
	if req != nil {
		if req.Target != "" {
			target = mailbox.NormalizeAddress(req.Target)
		}
		topicFilter = req.Topic
		ack = req.Ack
	}

	// Read from canonical mailbox prefix
	entries := store.List(mailbox.PrefixFor(target))

	var messages []mailbox.Message
	var keysToAck []string
	for _, entry := range entries {
		var msg mailbox.Message
		if err := json.Unmarshal(entry.Value, &msg); err == nil {
			if topicFilter != "" && !strings.EqualFold(msg.Topic, topicFilter) {
				continue
			}
			messages = append(messages, msg)
			keysToAck = append(keysToAck, entry.Key)
		}
	}

	if ack && len(keysToAck) > 0 {
		for _, k := range keysToAck {
			if delegate != nil {
				delegate.DeleteAndBroadcast(k)
			} else {
				store.Delete(k)
			}
		}
	}

	return &ipc.MailReadResponse{
		Messages: messages,
		Count:    len(messages),
		Acked:    ack,
	}, nil
}

