package subsys

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// CPTarget represents a parsed source or destination location for file transfer.
type CPTarget struct {
	Node     string
	Path     string
	IsRemote bool
}

// ParseCPTarget parses a string like "node-1:/tmp/file.txt" or "/local/file.txt".
func ParseCPTarget(spec string) CPTarget {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) == 2 && parts[0] != "" && !strings.HasPrefix(spec, "/") && !strings.HasPrefix(spec, ".") {
		return CPTarget{
			Node:     parts[0],
			Path:     parts[1],
			IsRemote: true,
		}
	}
	return CPTarget{
		Node:     "",
		Path:     spec,
		IsRemote: false,
	}
}

// CopyLocalFile copies a file from src to dst and returns bytes transferred.
func CopyLocalFile(src, dst string) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("failed to open source %s: %w", src, err)
	}
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat source %s: %w", src, err)
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return 0, fmt.Errorf("failed to open destination %s: %w", dst, err)
	}
	defer func() { _ = dstFile.Close() }()

	n, err := io.Copy(dstFile, srcFile)
	if err != nil {
		return n, fmt.Errorf("error during file copy: %w", err)
	}

	return n, nil
}

// FileTransferRequest defines the metadata header for StreamTypeFile transfers.
type FileTransferRequest struct {
	Action   string `json:"action"`              // "download" or "upload"
	Path     string `json:"path"`                // Target path on the server
	Size     int64  `json:"size,omitempty"`     // Byte count (for upload)
	FileMode uint32 `json:"file_mode,omitempty"`// File permissions
}

// FileTransferResponse reports the status of a file transfer request.
type FileTransferResponse struct {
	Success  bool   `json:"success"`
	Size     int64  `json:"size,omitempty"`
	FileMode uint32 `json:"file_mode,omitempty"`
	Error    string `json:"error,omitempty"`
}

// HandleFileStream processes an incoming StreamTypeFile connection on the server.
func HandleFileStream(conn net.Conn) error {
	defer func() { _ = conn.Close() }()

	var req FileTransferRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(FileTransferResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to decode request: %v", err),
		})
		return err
	}

	cleanPath := filepath.Clean(req.Path)
	if cleanPath == "" || cleanPath == "." {
		errResp := FileTransferResponse{
			Success: false,
			Error:   "invalid target path",
		}
		_ = json.NewEncoder(conn).Encode(errResp)
		return errors.New(errResp.Error)
	}

	switch req.Action {
	case "download":
		f, err := os.Open(cleanPath)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(FileTransferResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to open source %s: %v", cleanPath, err),
			})
			return err
		}
		defer func() { _ = f.Close() }()

		fi, err := f.Stat()
		if err != nil {
			_ = json.NewEncoder(conn).Encode(FileTransferResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to stat source %s: %v", cleanPath, err),
			})
			return err
		}
		if fi.IsDir() {
			_ = json.NewEncoder(conn).Encode(FileTransferResponse{
				Success: false,
				Error:   fmt.Sprintf("source %s is a directory, not a regular file", cleanPath),
			})
			return fmt.Errorf("source %s is a directory", cleanPath)
		}

		if err := json.NewEncoder(conn).Encode(FileTransferResponse{
			Success:  true,
			Size:     fi.Size(),
			FileMode: uint32(fi.Mode().Perm()),
		}); err != nil {
			return err
		}

		_, err = io.CopyN(conn, f, fi.Size())
		return err

	case "upload":
		dstDir := filepath.Dir(cleanPath)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			_ = json.NewEncoder(conn).Encode(FileTransferResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to create directory %s: %v", dstDir, err),
			})
			return err
		}

		mode := os.FileMode(req.FileMode)
		if mode == 0 {
			mode = 0644
		}
		dst, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(FileTransferResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to create file %s: %v", cleanPath, err),
			})
			return err
		}
		defer func() { _ = dst.Close() }()

		if err := json.NewEncoder(conn).Encode(FileTransferResponse{
			Success: true,
		}); err != nil {
			return err
		}

		n, err := io.CopyN(dst, conn, req.Size)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(FileTransferResponse{
				Success: false,
				Error:   fmt.Sprintf("upload interrupted after %d bytes: %v", n, err),
			})
			return err
		}

		return json.NewEncoder(conn).Encode(FileTransferResponse{
			Success: true,
			Size:    n,
		})

	default:
		errResp := FileTransferResponse{
			Success: false,
			Error:   fmt.Sprintf("unsupported action: %s", req.Action),
		}
		_ = json.NewEncoder(conn).Encode(errResp)
		return errors.New(errResp.Error)
	}
}

// DownloadFromStream requests and downloads a remote file over an active connection to a local path.
func DownloadFromStream(conn net.Conn, remotePath, localDstPath string) (int64, error) {
	req := FileTransferRequest{
		Action: "download",
		Path:   remotePath,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return 0, fmt.Errorf("failed to encode download request: %w", err)
	}

	var resp FileTransferResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return 0, fmt.Errorf("failed to read download response: %w", err)
	}
	if !resp.Success {
		return 0, fmt.Errorf("remote download error: %s", resp.Error)
	}

	dstDir := filepath.Dir(localDstPath)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create destination directory: %w", err)
	}

	mode := os.FileMode(resp.FileMode)
	if mode == 0 {
		mode = 0644
	}
	dstFile, err := os.OpenFile(localDstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, fmt.Errorf("failed to create local file: %w", err)
	}
	defer func() { _ = dstFile.Close() }()

	n, err := io.CopyN(dstFile, conn, resp.Size)
	if err != nil {
		return n, fmt.Errorf("failed to stream file content: %w", err)
	}
	return n, nil
}

// UploadToStream streams a local file over an active connection to a remote path.
func UploadToStream(conn net.Conn, localSrcPath, remoteDstPath string) (int64, error) {
	srcFile, err := os.Open(localSrcPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open source file %s: %w", localSrcPath, err)
	}
	defer func() { _ = srcFile.Close() }()

	fi, err := srcFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat source file %s: %w", localSrcPath, err)
	}
	if fi.IsDir() {
		return 0, fmt.Errorf("source %s is a directory, not a regular file", localSrcPath)
	}

	req := FileTransferRequest{
		Action:   "upload",
		Path:     remoteDstPath,
		Size:     fi.Size(),
		FileMode: uint32(fi.Mode().Perm()),
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return 0, fmt.Errorf("failed to encode upload request: %w", err)
	}

	var resp FileTransferResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return 0, fmt.Errorf("failed to read upload response: %w", err)
	}
	if !resp.Success {
		return 0, fmt.Errorf("remote upload rejected: %s", resp.Error)
	}

	n, err := io.CopyN(conn, srcFile, fi.Size())
	if err != nil {
		return n, fmt.Errorf("failed to stream upload data: %w", err)
	}

	var ack FileTransferResponse
	if err := json.NewDecoder(conn).Decode(&ack); err != nil {
		return n, fmt.Errorf("failed to read completion ack: %w", err)
	}
	if !ack.Success {
		return n, fmt.Errorf("remote write failed: %s", ack.Error)
	}

	return n, nil
}

