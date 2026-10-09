package proto

import (
	"encoding/binary"
	"fmt"
)

// Response represents a parsed BHTTP/1 response.
type Response struct {
	Status  uint16
	Headers []HeaderEntry
}

// DecodeResponsePayload parses a RESPONSE frame payload strictly.
func DecodeResponsePayload(payload []byte) (*Response, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("truncated response: missing status")
	}

	status := binary.BigEndian.Uint16(payload[:2])
	headers, n, err := DecodeHeaderBlock(payload[2:])
	if err != nil {
		return nil, fmt.Errorf("malformed header block in response: %w", err)
	}

	if 2+n != len(payload) {
		return nil, fmt.Errorf("trailing unparsed bytes in response payload (%d extra bytes)", len(payload)-(2+n))
	}

	return &Response{
		Status:  status,
		Headers: headers,
	}, nil
}

// EncodeResponsePayload serializes a Response into payload bytes.
func EncodeResponsePayload(resp *Response) ([]byte, error) {
	hdrBytes, err := EncodeHeaderBlock(resp.Headers)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 2+len(hdrBytes))
	binary.BigEndian.PutUint16(buf[:2], resp.Status)
	copy(buf[2:], hdrBytes)
	return buf, nil
}

// MakeErrorResponseFrame creates an error response frame (400/404/500) with END_STREAM and content-length "0".
func MakeErrorResponseFrame(requestID uint32, status uint16) Frame {
	payload, _ := EncodeResponsePayload(&Response{
		Status: status,
		Headers: []HeaderEntry{
			{Name: "content-length", Value: "0"},
		},
	})
	return Frame{
		Header: Header{
			Version:   Version,
			Type:      FrameTypeResponse,
			Flags:     FlagEndStream,
			Reserved:  0,
			RequestID: requestID,
			Length:    uint32(len(payload)),
		},
		Payload: payload,
	}
}
