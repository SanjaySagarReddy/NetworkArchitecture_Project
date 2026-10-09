package proto

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

const (
	// MethodGet is the only supported HTTP method in BHTTP/1.
	MethodGet uint8 = 0x01
)

// Request represents a parsed BHTTP/1 request.
type Request struct {
	Method  uint8
	Path    string
	Headers []HeaderEntry
}

// DecodeRequestPayload parses a REQUEST frame payload strictly.
func DecodeRequestPayload(payload []byte) (*Request, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("truncated request: missing method")
	}

	method := payload[0]
	if method != MethodGet {
		return nil, fmt.Errorf("unsupported or invalid method: 0x%02x (only 0x01 GET allowed)", method)
	}

	if len(payload) < 3 {
		return nil, fmt.Errorf("truncated request: missing path_len")
	}

	pathLen := int(binary.BigEndian.Uint16(payload[1:3]))
	if len(payload) < 3+pathLen {
		return nil, fmt.Errorf("truncated request: path_len is %d but payload has %d remaining", pathLen, len(payload)-3)
	}

	pathBytes := payload[3 : 3+pathLen]
	if pathLen < 1 || pathBytes[0] != '/' {
		return nil, fmt.Errorf("path must start with '/'")
	}

	if !utf8.Valid(pathBytes) {
		return nil, fmt.Errorf("path is not valid UTF-8")
	}

	pathStr := string(pathBytes)

	headers, n, err := DecodeHeaderBlock(payload[3+pathLen:])
	if err != nil {
		return nil, fmt.Errorf("malformed header block: %w", err)
	}

	if 3+pathLen+n != len(payload) {
		return nil, fmt.Errorf("trailing unparsed bytes in request payload (%d extra bytes)", len(payload)-(3+pathLen+n))
	}

	return &Request{
		Method:  method,
		Path:    pathStr,
		Headers: headers,
	}, nil
}

// EncodeRequestPayload serializes a Request into payload bytes.
func EncodeRequestPayload(req *Request) ([]byte, error) {
	if req.Method != MethodGet {
		return nil, fmt.Errorf("unsupported method: 0x%02x", req.Method)
	}
	pathBytes := []byte(req.Path)
	if len(pathBytes) > 65535 {
		return nil, fmt.Errorf("path too long: %d bytes", len(pathBytes))
	}
	if len(pathBytes) == 0 || pathBytes[0] != '/' {
		return nil, fmt.Errorf("path must start with '/'")
	}

	hdrBytes, err := EncodeHeaderBlock(req.Headers)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 1+2+len(pathBytes)+len(hdrBytes))
	buf[0] = req.Method
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(pathBytes)))
	copy(buf[3:], pathBytes)
	copy(buf[3+len(pathBytes):], hdrBytes)

	return buf, nil
}
