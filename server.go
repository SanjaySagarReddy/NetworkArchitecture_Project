package main

import (
	"bserve/proto"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server represents a BHTTP/1 server instance.
type Server struct {
	RootDir       string
	AbsRootDir    string
	Port          string
	InjectUnknown bool
	Verbose       bool

	listener net.Listener
	wg       sync.WaitGroup
	quit     chan struct{}
}

// NewServer initializes a Server instance.
func NewServer(rootDir, port string, injectUnknown, verbose bool) (*Server, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("invalid root directory %s: %w", rootDir, err)
	}
	absRoot = filepath.Clean(absRoot)

	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("root directory %s does not exist: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root directory %s is not a directory", absRoot)
	}

	return &Server{
		RootDir:       rootDir,
		AbsRootDir:    absRoot,
		Port:          port,
		InjectUnknown: injectUnknown,
		Verbose:       verbose,
		quit:          make(chan struct{}),
	}, nil
}

// Start begins listening on TCP and serving connections.
func (s *Server) Start() error {
	addr := ":" + s.Port
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = l
	return s.Serve(l)
}

// Serve accepts incoming connections on l.
func (s *Server) Serve(l net.Listener) error {
	s.listener = l
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return nil
			default:
				return err
			}
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConnection(c)
		}(conn)
	}
}

// Close shuts down the server.
func (s *Server) Close() error {
	close(s.quit)
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// logRequest outputs: remote addr, request_id, path, status, bytes
func logRequest(remoteAddr string, reqID uint32, path string, status uint16, bytes int64) {
	fmt.Fprintf(os.Stderr, "%s, %d, %s, %d, %d\n", remoteAddr, reqID, path, status, bytes)
}

// sendRawFrame writes a frame to conn, respecting verbose logging.
func (s *Server) sendRawFrame(conn net.Conn, f proto.Frame) error {
	if s.Verbose {
		proto.DumpFrame(os.Stderr, "> ", f.Header, f.Payload)
	}
	return f.WriteTo(conn)
}

// sendResponseFrame sends a response frame, injecting unknown frame if configured.
func (s *Server) sendResponseFrame(conn net.Conn, f proto.Frame) error {
	if s.InjectUnknown {
		// Before every RESPONSE, send a frame of type 0x7F with an 8-byte junk payload
		junkPayload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}
		junkFrame := proto.Frame{
			Header: proto.Header{
				Version:   proto.Version,
				Type:      0x7F,
				Flags:     0x00,
				Reserved:  0x00,
				RequestID: f.Header.RequestID,
				Length:    uint32(len(junkPayload)),
			},
			Payload: junkPayload,
		}
		if err := s.sendRawFrame(conn, junkFrame); err != nil {
			return err
		}
	}
	return s.sendRawFrame(conn, f)
}

// sendError sends an error response (400, 404, 500) with END_STREAM and content-length "0".
func (s *Server) sendError(conn net.Conn, reqID uint32, status uint16) error {
	frame := proto.MakeErrorResponseFrame(reqID, status)
	return s.sendResponseFrame(conn, frame)
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	remoteAddr := conn.RemoteAddr().String()

	for {
		header, err := proto.ReadHeader(conn)
		if err != nil {
			// io.EOF means a clean exit, not an error
			return
		}

		// Framing errors (§6): version != 1, length > 1 MiB
		if header.Version != proto.Version || header.Length > proto.MaxPayloadLength {
			logRequest(remoteAddr, 0, "-", 400, 0)
			_ = s.sendError(conn, 0, 400)
			// Drain any already-buffered bytes so TCP stack sends FIN instead of RST on close
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			var discardBuf [4096]byte
			for {
				_, err := conn.Read(discardBuf[:])
				if err != nil {
					break
				}
			}
			return // Cannot resynchronise; close connection immediately
		}

		payload := make([]byte, header.Length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			// Truncated payload before frame completion: connection broken
			return
		}

		if s.Verbose {
			proto.DumpFrame(os.Stderr, "< ", header, payload)
		}

		// Dispatch on type
		if header.Type != proto.FrameTypeRequest {
			// §5 Unknown frames: discard length bytes and continue
			continue
		}

		// Parse REQUEST per §3/§4
		req, err := proto.DecodeRequestPayload(payload)
		if err != nil {
			// Malformed REQUEST payload: reply 400 with same request_id, keep connection open
			logRequest(remoteAddr, header.RequestID, "-", 400, 0)
			if err := s.sendError(conn, header.RequestID, 400); err != nil {
				return
			}
			continue
		}

		// Process valid request
		s.serveRequest(conn, remoteAddr, header.RequestID, req)
	}
}

func (s *Server) serveRequest(conn net.Conn, remoteAddr string, reqID uint32, req *proto.Request) {
	reqPath := req.Path
	if reqPath == "/" {
		reqPath = "/index.html"
	}

	// Path mapping: filepath.Clean, verify result stays under absolute root
	cleanRel := filepath.Clean("." + reqPath)
	// Check if path escaped root
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		logRequest(remoteAddr, reqID, req.Path, 400, 0)
		_ = s.sendError(conn, reqID, 400)
		return
	}

	targetPath := filepath.Clean(filepath.Join(s.AbsRootDir, cleanRel))
	relToRoot, err := filepath.Rel(s.AbsRootDir, targetPath)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		logRequest(remoteAddr, reqID, req.Path, 400, 0)
		_ = s.sendError(conn, reqID, 400)
		return
	}

	fi, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			logRequest(remoteAddr, reqID, req.Path, 404, 0)
			_ = s.sendError(conn, reqID, 404)
			return
		}
		// Read errors get 500
		logRequest(remoteAddr, reqID, req.Path, 500, 0)
		_ = s.sendError(conn, reqID, 500)
		return
	}

	if fi.IsDir() {
		// A directory gets 404
		logRequest(remoteAddr, reqID, req.Path, 404, 0)
		_ = s.sendError(conn, reqID, 404)
		return
	}

	file, err := os.Open(targetPath)
	if err != nil {
		logRequest(remoteAddr, reqID, req.Path, 500, 0)
		_ = s.sendError(conn, reqID, 500)
		return
	}
	defer file.Close()

	fileSize := fi.Size()
	contentType := getContentType(targetPath)

	respPayload, err := proto.EncodeResponsePayload(&proto.Response{
		Status: 200,
		Headers: []proto.HeaderEntry{
			{Name: "content-type", Value: contentType},
			{Name: "content-length", Value: strconv.FormatInt(fileSize, 10)},
			{Name: "server", Value: "bserve/1.0"},
		},
	})
	if err != nil {
		logRequest(remoteAddr, reqID, req.Path, 500, 0)
		_ = s.sendError(conn, reqID, 500)
		return
	}

	// 200 RESPONSE frame: flags = 0 (END_STREAM not set, DATA frames follow)
	respFrame := proto.Frame{
		Header: proto.Header{
			Version:   proto.Version,
			Type:      proto.FrameTypeResponse,
			Flags:     0x00,
			Reserved:  0x00,
			RequestID: reqID,
			Length:    uint32(len(respPayload)),
		},
		Payload: respPayload,
	}

	if err := s.sendResponseFrame(conn, respFrame); err != nil {
		return
	}

	// Stream DATA frames: at most 16,384 bytes per frame
	// A zero-length file gets one empty DATA frame with END_STREAM
	if fileSize == 0 {
		emptyDataFrame := proto.Frame{
			Header: proto.Header{
				Version:   proto.Version,
				Type:      proto.FrameTypeData,
				Flags:     proto.FlagEndStream,
				Reserved:  0x00,
				RequestID: reqID,
				Length:    0,
			},
			Payload: nil,
		}
		if err := s.sendRawFrame(conn, emptyDataFrame); err != nil {
			return
		}
	} else {
		buf := make([]byte, proto.MaxDataChunkSize)
		var bytesRemaining = fileSize

		for bytesRemaining > 0 {
			toRead := int64(proto.MaxDataChunkSize)
			if bytesRemaining < toRead {
				toRead = bytesRemaining
			}

			n, err := io.ReadFull(file, buf[:toRead])
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return
			}
			bytesRemaining -= int64(n)

			flags := uint8(0)
			if bytesRemaining == 0 {
				flags = proto.FlagEndStream
			}

			dataFrame := proto.Frame{
				Header: proto.Header{
					Version:   proto.Version,
					Type:      proto.FrameTypeData,
					Flags:     flags,
					Reserved:  0x00,
					RequestID: reqID,
					Length:    uint32(n),
				},
				Payload: buf[:n],
			}

			if err := s.sendRawFrame(conn, dataFrame); err != nil {
				return
			}
		}
	}

	logRequest(remoteAddr, reqID, req.Path, 200, fileSize)
}

func getContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".json":
		return "application/json"
	case ".txt":
		return "text/plain"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
