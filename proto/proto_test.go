package proto

import (
	"bytes"
	"testing"
)

// Test 1: The exact request bytes in SPEC §8 decode to GET /index.html with the two headers.
func TestSpecSection8RequestDecode(t *testing.T) {
	reqBytes := []byte{
		// Header (12 bytes)
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x2c,
		// Payload (44 bytes)
		0x01,                   // method = GET
		0x00, 0x0b,             // path_len = 11
		0x2f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x2e, 0x68, 0x74, 0x6d, 0x6c, // "/index.html"
		0x02,                   // count = 2 headers
		0x83, 0x00, 0x0e, 0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x68, 0x6f, 0x73, 0x74, 0x3a, 0x39, 0x30, 0x30, 0x30, // [3]host = "localhost:9000"
		0x84, 0x00, 0x09, 0x62, 0x63, 0x75, 0x72, 0x6c, 0x2f, 0x31, 0x2e, 0x30, // [4]user-agent = "bcurl/1.0"
	}

	r := bytes.NewReader(reqBytes)
	header, err := ReadHeader(r)
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}

	if header.Version != 1 {
		t.Errorf("expected version 1, got %d", header.Version)
	}
	if header.Type != FrameTypeRequest {
		t.Errorf("expected type REQUEST, got %d", header.Type)
	}
	if header.Flags != 0 {
		t.Errorf("expected flags 0, got %d", header.Flags)
	}
	if header.RequestID != 1 {
		t.Errorf("expected request_id 1, got %d", header.RequestID)
	}
	if header.Length != 44 {
		t.Errorf("expected length 44, got %d", header.Length)
	}

	payload := make([]byte, header.Length)
	if _, err := r.Read(payload); err != nil {
		t.Fatalf("failed reading payload: %v", err)
	}

	req, err := DecodeRequestPayload(payload)
	if err != nil {
		t.Fatalf("DecodeRequestPayload failed: %v", err)
	}

	if req.Method != MethodGet {
		t.Errorf("expected method GET (0x01), got 0x%02x", req.Method)
	}
	if req.Path != "/index.html" {
		t.Errorf("expected path /index.html, got %q", req.Path)
	}
	if len(req.Headers) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(req.Headers))
	}
	if req.Headers[0].Name != "host" || req.Headers[0].Value != "localhost:9000" {
		t.Errorf("expected host header 'localhost:9000', got %v", req.Headers[0])
	}
	if req.Headers[1].Name != "user-agent" || req.Headers[1].Value != "bcurl/1.0" {
		t.Errorf("expected user-agent header 'bcurl/1.0', got %v", req.Headers[1])
	}
}

// Test 2: Encoding a 404 with content-length "0" produces exactly the response bytes in SPEC §8.
func TestSpecSection8Response404Encode(t *testing.T) {
	expectedBytes := []byte{
		// Header (12 bytes)
		0x01, 0x02, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x07,
		// Payload (7 bytes)
		0x01, 0x94,             // status = 404
		0x01,                   // count = 1 header
		0x82, 0x00, 0x01, 0x30, // [2]content-length = "0"
	}

	frame := MakeErrorResponseFrame(1, 404)
	actualBytes := frame.Encode()

	if !bytes.Equal(actualBytes, expectedBytes) {
		t.Fatalf("bytes mismatch:\nexpected: %x\nactual:   %x", expectedBytes, actualBytes)
	}
}
