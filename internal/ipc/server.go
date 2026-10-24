package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Server implements a Unix domain socket IPC server.
type Server struct {
	socketPath string
	handler    DaemonHandler
	listener   net.Listener
	requestCtx context.Context
	ctxMu      sync.RWMutex
	mu         sync.Mutex
	closed     atomic.Bool
	wg         sync.WaitGroup
}

// NewServer creates a new IPC Server.
func NewServer(socketPath string, handler DaemonHandler) *Server {
	return &Server{
		socketPath: socketPath,
		handler:    handler,
		requestCtx: context.Background(),
	}
}

// SetRequestContext configures the context passed to daemon IPC handlers.
func (s *Server) SetRequestContext(ctx context.Context) {
	if ctx != nil {
		s.ctxMu.Lock()
		s.requestCtx = ctx
		s.ctxMu.Unlock()
	}
}

// baseRequestContext returns a cancellation-detached snapshot of the base
// context so Stop/runCancel cannot poison in-flight handlers mid-flight.
// Values are preserved; per-connection/per-request children add their own
// cancellation tied to the client connection lifetime.
func (s *Server) baseRequestContext() context.Context {
	s.ctxMu.RLock()
	base := s.requestCtx
	s.ctxMu.RUnlock()
	if base == nil {
		return context.Background()
	}
	return context.WithoutCancel(base)
}

// Start begins listening on the Unix domain socket.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure directory exists
	socketDir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Clean up stale socket if it exists
	if _, err := os.Stat(s.socketPath); err == nil {
		// Test if actively in use
		if conn, err := net.Dial("unix", s.socketPath); err == nil {
			_ = conn.Close()
			return fmt.Errorf("socket %s is already in use by another running daemon", s.socketPath)
		}
		_ = os.Remove(s.socketPath)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket %s: %w", s.socketPath, err)
	}

	// Ensure socket permissions are 0600
	_ = os.Chmod(s.socketPath, 0600)

	s.listener = listener
	s.closed.Store(false)

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.closed.Load() {
				return
			}
			continue
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() { _ = c.Close() }()
			s.handleConnection(c)
		}(conn)
	}
}

// agentStreamer is an optional DaemonHandler extension for streaming chat.
// Handlers that implement it serve AgentPrompt requests with Stream=true as
// a sequence of AgentPromptChunk frames over the same Unix socket.
type agentStreamer interface {
	AgentPromptStream(ctx context.Context, req *AgentPromptRequest, yield func(chunk AgentPromptChunk) bool) (*AgentPromptResponse, error)
}

func (s *Server) handleConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Connection-scoped context: canceled when the client disconnects,
	// independent of daemon Stop/runCancel (see baseRequestContext).
	connCtx, connCancel := context.WithCancel(s.baseRequestContext())
	defer connCancel()

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(writer, Response{Success: false, Error: "invalid request payload: " + err.Error()})
			continue
		}

		if s.tryServeAgentPromptStream(writer, connCtx, req) {
			continue
		}

		reqCtx, reqCancel := context.WithCancel(connCtx)
		resp := s.dispatch(reqCtx, req)
		reqCancel()
		s.writeResponse(writer, resp)
	}
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	if resp, handled := s.dispatchCluster(ctx, req); handled {
		return resp
	}
	if resp, handled := s.dispatchSubsys(ctx, req); handled {
		return resp
	}
	if resp, handled := s.dispatchKV(ctx, req); handled {
		return resp
	}
	if resp, handled := s.dispatchAgent(ctx, req); handled {
		return resp
	}
	if resp, handled := s.dispatchMail(ctx, req); handled {
		return resp
	}
	return Response{Success: false, Error: fmt.Sprintf("unknown method: %s", req.Method)}
}

// tryServeAgentPromptStream handles AgentPrompt requests with Stream=true by
// writing a sequence of AgentPromptChunk frames over the same Unix socket.
// It returns false when the request is not a streaming agent prompt or the
// handler does not implement streaming, in which case the caller falls back
// to the unary dispatch path so one-shot prompts stay unchanged.
func (s *Server) tryServeAgentPromptStream(writer *bufio.Writer, connCtx context.Context, req Request) bool {
	switch req.Method {
	case "AgentPrompt", "agent_prompt", "agent.prompt":
	default:
		return false
	}
	if len(req.Params) == 0 {
		return false
	}
	var pReq AgentPromptRequest
	if err := json.Unmarshal(req.Params, &pReq); err != nil {
		return false
	}
	if !pReq.Stream {
		return false
	}
	streamer, ok := s.handler.(agentStreamer)
	if !ok {
		// Backward-compatible fallback: run the unary handler and wrap the
		// full response in a single Done chunk so streaming clients still
		// terminate without requiring a new port or method.
		reqCtx, reqCancel := context.WithCancel(connCtx)
		defer reqCancel()
		pResp, err := s.handler.AgentPrompt(reqCtx, &pReq)
		if err != nil {
			s.writeResponse(writer, Response{Success: false, Error: err.Error()})
			return true
		}
		done := AgentPromptChunk{Done: true, Final: pResp}
		raw, merr := json.Marshal(done)
		if merr != nil {
			s.writeResponse(writer, Response{Success: false, Error: "failed to marshal final chunk: " + merr.Error()})
			return true
		}
		s.writeResponse(writer, Response{Success: true, Result: raw})
		return true
	}
	reqCtx, reqCancel := context.WithCancel(connCtx)
	defer reqCancel()
	writeChunk := func(chunk AgentPromptChunk) bool {
		raw, err := json.Marshal(chunk)
		if err != nil {
			return false
		}
		s.writeResponse(writer, Response{Success: true, Result: raw})
		return true
	}
	final, err := streamer.AgentPromptStream(reqCtx, &pReq, writeChunk)
	if err != nil {
		s.writeResponse(writer, Response{Success: false, Error: err.Error()})
		return true
	}
	done := AgentPromptChunk{Done: true, Final: final}
	raw, err := json.Marshal(done)
	if err != nil {
		s.writeResponse(writer, Response{Success: false, Error: "failed to marshal final chunk: " + err.Error()})
		return true
	}
	s.writeResponse(writer, Response{Success: true, Result: raw})
	return true
}

func (s *Server) dispatchMail(ctx context.Context, req Request) (Response, bool) {
	switch req.Method {
	case "MailSend", "mail_send", "mail.send":
		var mReq MailSendRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &mReq); err != nil {
				return Response{Success: false, Error: "invalid mail_send params: " + err.Error()}, true
			}
		}
		resp, err := s.handler.MailSend(ctx, &mReq)
		return toResponse(resp, err), true

	case "MailList", "mail_list", "mail.list":
		var mReq MailListRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &mReq); err != nil {
				return Response{Success: false, Error: "invalid mail_list params: " + err.Error()}, true
			}
		}
		resp, err := s.handler.MailList(ctx, &mReq)
		return toResponse(resp, err), true

	case "MailRead", "mail_read", "mail.read":
		var mReq MailReadRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &mReq); err != nil {
				return Response{Success: false, Error: "invalid mail_read params: " + err.Error()}, true
			}
		}
		resp, err := s.handler.MailRead(ctx, &mReq)
		return toResponse(resp, err), true

	default:
		return Response{}, false
	}
}

func (s *Server) dispatchAgent(ctx context.Context, req Request) (Response, bool) {
	switch req.Method {
	case "AgentPrompt", "agent_prompt", "agent.prompt":
		var pReq AgentPromptRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &pReq); err != nil {
				return Response{Success: false, Error: "invalid agent_prompt params: " + err.Error()}, true
			}
		}
		pResp, err := s.handler.AgentPrompt(ctx, &pReq)
		return toResponse(pResp, err), true

	case "AgentStatus", "agent_status", "agent.status":
		var sReq AgentStatusRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &sReq); err != nil {
				return Response{Success: false, Error: "invalid agent_status params: " + err.Error()}, true
			}
		}
		sResp, err := s.handler.AgentStatus(ctx, &sReq)
		return toResponse(sResp, err), true

	case "AgentListSessions", "agent_list_sessions", "agent.list_sessions", "agent.sessions":
		var lReq AgentListSessionsRequest
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &lReq)
		}
		lResp, err := s.handler.AgentListSessions(ctx, &lReq)
		return toResponse(lResp, err), true

	default:
		return Response{}, false
	}
}

func (s *Server) dispatchCluster(ctx context.Context, req Request) (Response, bool) {
	switch req.Method {
	case "GetStatus", "status":
		status, err := s.handler.GetStatus(ctx)
		return toResponse(status, err), true

	case "GetRoster", "roster":
		roster, err := s.handler.GetRoster(ctx)
		return toResponse(roster, err), true

	case "JoinNode", "join":
		var joinReq JoinRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &joinReq); err != nil {
				return Response{Success: false, Error: "invalid join params: " + err.Error()}, true
			}
		}
		joinResp, err := s.handler.JoinNode(ctx, joinReq.Address)
		return toResponse(joinResp, err), true

	case "LeaveCluster", "leave":
		leaveResp, err := s.handler.LeaveCluster(ctx)
		return toResponse(leaveResp, err), true

	case "StopDaemon", "stop":
		stopResp, err := s.handler.StopDaemon(ctx)
		return toResponse(stopResp, err), true

	default:
		return Response{}, false
	}
}

func (s *Server) dispatchSubsys(ctx context.Context, req Request) (Response, bool) {
	switch req.Method {
	case "ExecCommand", "exec":
		var execReq ExecRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &execReq); err != nil {
				return Response{Success: false, Error: "invalid exec params: " + err.Error()}, true
			}
		}
		execResp, err := s.handler.ExecCommand(ctx, &execReq)
		return toResponse(execResp, err), true

	case "ForwardPort", "forward":
		var fwdReq ForwardRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &fwdReq); err != nil {
				return Response{Success: false, Error: "invalid forward params: " + err.Error()}, true
			}
		}
		fwdResp, err := s.handler.ForwardPort(ctx, &fwdReq)
		return toResponse(fwdResp, err), true

	case "CopyFile", "cp":
		var cpReq CPRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &cpReq); err != nil {
				return Response{Success: false, Error: "invalid cp params: " + err.Error()}, true
			}
		}
		cpResp, err := s.handler.CopyFile(ctx, &cpReq)
		return toResponse(cpResp, err), true

	default:
		return Response{}, false
	}
}

func (s *Server) dispatchKV(ctx context.Context, req Request) (Response, bool) {
	switch req.Method {
	case "KVGet", "kv_get":
		var getReq KVGetRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &getReq); err != nil {
				return Response{Success: false, Error: "invalid kv_get params: " + err.Error()}, true
			}
		}
		getResp, err := s.handler.KVGet(ctx, &getReq)
		return toResponse(getResp, err), true

	case "KVSet", "kv_set":
		var setReq KVSetRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &setReq); err != nil {
				return Response{Success: false, Error: "invalid kv_set params: " + err.Error()}, true
			}
		}
		setResp, err := s.handler.KVSet(ctx, &setReq)
		return toResponse(setResp, err), true

	case "KVDelete", "kv_delete":
		var delReq KVDeleteRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &delReq); err != nil {
				return Response{Success: false, Error: "invalid kv_delete params: " + err.Error()}, true
			}
		}
		delResp, err := s.handler.KVDelete(ctx, &delReq)
		return toResponse(delResp, err), true

	case "KVList", "kv_list":
		var listReq KVListRequest
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &listReq); err != nil {
				return Response{Success: false, Error: "invalid kv_list params: " + err.Error()}, true
			}
		}
		listResp, err := s.handler.KVList(ctx, &listReq)
		return toResponse(listResp, err), true

	default:
		return Response{}, false
	}
}

func toResponse(result any, err error) Response {
	if err != nil {
		return Response{
			Success: false,
			Error:   err.Error(),
		}
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return Response{
			Success: false,
			Error:   "failed to marshal result: " + marshalErr.Error(),
		}
	}
	return Response{
		Success: true,
		Result:  raw,
	}
}

func (s *Server) writeResponse(w *bufio.Writer, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
	_ = w.Flush()
}

// Stop stops the server, closes connections, and cleans up the socket file.
func (s *Server) Stop() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	s.mu.Lock()
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()

	if removeErr := os.Remove(s.socketPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		if err == nil {
			err = removeErr
		}
	}

	return err
}
