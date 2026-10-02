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
	mu         sync.Mutex
	closed     atomic.Bool
	wg         sync.WaitGroup
}

// NewServer creates a new IPC Server.
func NewServer(socketPath string, handler DaemonHandler) *Server {
	return &Server{
		socketPath: socketPath,
		handler:    handler,
	}
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

func (s *Server) handleConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

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

		resp := s.dispatch(context.Background(), req)
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
