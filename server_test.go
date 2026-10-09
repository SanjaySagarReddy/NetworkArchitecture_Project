package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Helper to start a test server listening on an ephemeral port.
func startTestServer(t *testing.T, rootDir string) (net.Listener, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		t.Fatalf("filepath.Abs failed: %v", err)
	}

	srv := &Server{
		RootDir:       rootDir,
		AbsRootDir:    filepath.Clean(absRoot),
		Port:          "",
		InjectUnknown: false,
		Verbose:       false,
		quit:          make(chan struct{}),
	}

	go func() {
		_ = srv.Serve(l)
	}()

	t.Cleanup(func() {
		_ = srv.Close()
	})

	return l, l.Addr().String()
}

// readFrameRaw reads a 12-byte header and payload from conn.
func readFrameRaw(t *testing.T, r io.Reader) ([]byte, []byte) {
	t.Helper()
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		t.Fatalf("failed to read 12-byte header: %v", err)
	}
	length := binary.BigEndian.Uint32(header[8:12])
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			t.Fatalf("failed to read payload of length %d: %v", length, err)
		}
	}
	return header, payload
}

// Test 3: End-to-end over a real socket with a temp root: two GETs on ONE connection (200 then 404), connection still open afterwards.
func TestEndToEndTwoRequestsOneConnection(t *testing.T) {
	tempDir := t.TempDir()
	indexFile := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(indexFile, []byte("<h1>Hello BHTTP</h1>\n"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	_, addr := startTestServer(t, tempDir)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// 1. Hand-written request 1: GET /index.html (id=1, 0 headers)
	// Header: ver=1, type=1, flags=0, rsv=0, id=1, len=15
	// Payload: method=1, path_len=11, path="/index.html", count=0
	req1 := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x0f,
		0x01,                   // GET
		0x00, 0x0b,             // path_len=11
		0x2f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x2e, 0x68, 0x74, 0x6d, 0x6c, // "/index.html"
		0x00,                   // 0 headers
	}
	if _, err := conn.Write(req1); err != nil {
		t.Fatalf("failed to write req1: %v", err)
	}

	// Read RESPONSE frame
	hdr1, pay1 := readFrameRaw(t, conn)
	if hdr1[0] != 0x01 { // version
		t.Errorf("expected version 1, got %d", hdr1[0])
	}
	if hdr1[1] != 0x02 { // type RESPONSE
		t.Errorf("expected RESPONSE frame (0x02), got 0x%02x", hdr1[1])
	}
	if hdr1[2] != 0x00 { // flags: END_STREAM should not be set because body follows
		t.Errorf("expected flags 0 on 200 RESPONSE, got 0x%02x", hdr1[2])
	}
	if binary.BigEndian.Uint32(hdr1[4:8]) != 1 { // request_id
		t.Errorf("expected request_id 1, got %d", binary.BigEndian.Uint32(hdr1[4:8]))
	}
	status1 := binary.BigEndian.Uint16(pay1[:2])
	if status1 != 200 {
		t.Fatalf("expected status 200, got %d", status1)
	}

	// Read DATA frame
	dataHdr1, dataPay1 := readFrameRaw(t, conn)
	if dataHdr1[1] != 0x03 { // type DATA
		t.Errorf("expected DATA frame (0x03), got 0x%02x", dataHdr1[1])
	}
	if dataHdr1[2] != 0x01 { // flags: END_STREAM
		t.Errorf("expected END_STREAM flag on DATA frame, got 0x%02x", dataHdr1[2])
	}
	if !bytes.Equal(dataPay1, []byte("<h1>Hello BHTTP</h1>\n")) {
		t.Errorf("body mismatch: got %q", string(dataPay1))
	}

	// 2. Hand-written request 2 on SAME connection: GET /missing.txt (id=2, 0 headers)
	// Header: ver=1, type=1, flags=0, rsv=0, id=2, len=16
	// Payload: method=1, path_len=12, path="/missing.txt", count=0
	req2 := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x10,
		0x01,                   // GET
		0x00, 0x0c,             // path_len=12
		0x2f, 0x6d, 0x69, 0x73, 0x73, 0x69, 0x6e, 0x67, 0x2e, 0x74, 0x78, 0x74, // "/missing.txt"
		0x00,                   // 0 headers
	}
	if _, err := conn.Write(req2); err != nil {
		t.Fatalf("failed to write req2: %v", err)
	}

	// Read RESPONSE frame for 404
	hdr2, pay2 := readFrameRaw(t, conn)
	if hdr2[1] != 0x02 { // type RESPONSE
		t.Errorf("expected RESPONSE frame (0x02), got 0x%02x", hdr2[1])
	}
	if hdr2[2] != 0x01 { // flags: END_STREAM
		t.Errorf("expected END_STREAM on 404, got 0x%02x", hdr2[2])
	}
	if binary.BigEndian.Uint32(hdr2[4:8]) != 2 { // request_id
		t.Errorf("expected request_id 2, got %d", binary.BigEndian.Uint32(hdr2[4:8]))
	}
	status2 := binary.BigEndian.Uint16(pay2[:2])
	if status2 != 404 {
		t.Fatalf("expected status 404, got %d", status2)
	}

	// Check connection is still open: a read with timeout should not return EOF
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var dummy [1]byte
	n, err := conn.Read(dummy[:])
	if err == io.EOF {
		t.Fatalf("connection was prematurely closed by server")
	}
	if n > 0 {
		t.Fatalf("unexpected data received: %x", dummy[:n])
	}
}

// Test 4: A 0x7F frame sent before a valid REQUEST is skipped, and the request is still answered.
func TestUnknownFrameTypeSkipped(t *testing.T) {
	tempDir := t.TempDir()
	indexFile := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(indexFile, []byte("Content"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	_, addr := startTestServer(t, tempDir)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Hand-written unknown frame 0x7F with 8-byte payload
	// Header: ver=1, type=0x7F, flags=0, rsv=0, id=1, len=8
	// Payload: 8 junk bytes
	unknownFrame := []byte{
		0x01, 0x7f, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x08,
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22,
	}

	// Hand-written valid REQUEST (id=1, GET /index.html)
	validReq := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x0f,
		0x01,                   // GET
		0x00, 0x0b,             // path_len=11
		0x2f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x2e, 0x68, 0x74, 0x6d, 0x6c,
		0x00,                   // 0 headers
	}

	// Send unknown frame followed by valid request
	if _, err := conn.Write(unknownFrame); err != nil {
		t.Fatalf("failed to write unknown frame: %v", err)
	}
	if _, err := conn.Write(validReq); err != nil {
		t.Fatalf("failed to write valid request: %v", err)
	}

	// Expect response to valid request (0x7F frame was skipped)
	hdr, pay := readFrameRaw(t, conn)
	if hdr[1] != 0x02 {
		t.Fatalf("expected RESPONSE frame (0x02), got 0x%02x", hdr[1])
	}
	status := binary.BigEndian.Uint16(pay[:2])
	if status != 200 {
		t.Fatalf("expected status 200, got %d", status)
	}

	// Read body DATA frame
	dataHdr, dataPay := readFrameRaw(t, conn)
	if dataHdr[1] != 0x03 {
		t.Fatalf("expected DATA frame, got 0x%02x", dataHdr[1])
	}
	if !bytes.Equal(dataPay, []byte("Content")) {
		t.Fatalf("body mismatch: got %q", string(dataPay))
	}
}

// Test 5: Truncated REQUEST payload gets a 400, and a following valid request on the same connection still works.
func TestTruncatedRequestGets400AndConnectionStaysOpen(t *testing.T) {
	tempDir := t.TempDir()
	indexFile := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(indexFile, []byte("OK"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	_, addr := startTestServer(t, tempDir)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Hand-written truncated REQUEST payload
	// Header: ver=1, type=1, flags=0, rsv=0, id=1, len=10
	// Payload: method=1, path_len=20 (but only 7 bytes follow, so truncated!)
	truncatedReq := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x0a,
		0x01,                         // method GET
		0x00, 0x14,                   // path_len=20
		0x2f, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, // only 7 bytes of path provided
	}

	if _, err := conn.Write(truncatedReq); err != nil {
		t.Fatalf("failed to write truncated req: %v", err)
	}

	// Must get 400 RESPONSE with same request_id=1 and END_STREAM
	hdr1, pay1 := readFrameRaw(t, conn)
	if hdr1[1] != 0x02 {
		t.Fatalf("expected RESPONSE frame, got 0x%02x", hdr1[1])
	}
	if hdr1[2] != 0x01 {
		t.Errorf("expected END_STREAM on 400, got 0x%02x", hdr1[2])
	}
	if binary.BigEndian.Uint32(hdr1[4:8]) != 1 {
		t.Errorf("expected request_id 1, got %d", binary.BigEndian.Uint32(hdr1[4:8]))
	}
	status1 := binary.BigEndian.Uint16(pay1[:2])
	if status1 != 400 {
		t.Fatalf("expected status 400, got %d", status1)
	}

	// Next, valid request on SAME connection: id=2, GET /index.html
	validReq := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x0f,
		0x01,                   // GET
		0x00, 0x0b,             // path_len=11
		0x2f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x2e, 0x68, 0x74, 0x6d, 0x6c,
		0x00,                   // 0 headers
	}
	if _, err := conn.Write(validReq); err != nil {
		t.Fatalf("failed to write valid request: %v", err)
	}

	// Expect 200 RESPONSE
	hdr2, pay2 := readFrameRaw(t, conn)
	if hdr2[1] != 0x02 {
		t.Fatalf("expected RESPONSE frame, got 0x%02x", hdr2[1])
	}
	if binary.BigEndian.Uint32(hdr2[4:8]) != 2 {
		t.Errorf("expected request_id 2, got %d", binary.BigEndian.Uint32(hdr2[4:8]))
	}
	status2 := binary.BigEndian.Uint16(pay2[:2])
	if status2 != 200 {
		t.Fatalf("expected status 200, got %d", status2)
	}
}

// Test 6: "/../etc/passwd" gets a 400.
func TestPathTraversalGets400(t *testing.T) {
	tempDir := t.TempDir()
	indexFile := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(indexFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	_, addr := startTestServer(t, tempDir)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Hand-written request: path = "/../etc/passwd" (len 14)
	// Header: ver=1, type=1, flags=0, rsv=0, id=1, len=18
	// Payload: method=1, path_len=14, path="/../etc/passwd", 0 headers
	req := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x12,
		0x01,                         // GET
		0x00, 0x0e,                   // path_len=14
		0x2f, 0x2e, 0x2e, 0x2f, 0x65, 0x74, 0x63, 0x2f, 0x70, 0x61, 0x73, 0x73, 0x77, 0x64, // "/../etc/passwd"
		0x00,                         // 0 headers
	}

	if _, err := conn.Write(req); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	hdr, pay := readFrameRaw(t, conn)
	if hdr[1] != 0x02 { // RESPONSE
		t.Fatalf("expected RESPONSE frame, got 0x%02x", hdr[1])
	}
	if hdr[2] != 0x01 { // END_STREAM
		t.Errorf("expected END_STREAM flag on 400, got 0x%02x", hdr[2])
	}
	if binary.BigEndian.Uint32(hdr[4:8]) != 1 {
		t.Errorf("expected request_id 1, got %d", binary.BigEndian.Uint32(hdr[4:8]))
	}
	status := binary.BigEndian.Uint16(pay[:2])
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
}

// Test 7: version=2 gets a 400 with id 0 and the connection is closed.
func TestVersion2Gets400WithId0AndConnectionClosed(t *testing.T) {
	tempDir := t.TempDir()
	_, addr := startTestServer(t, tempDir)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Hand-written frame with version=2
	// Header: ver=2, type=1, flags=0, rsv=0, id=1, len=4
	// Payload: 4 bytes
	badVerFrame := []byte{
		0x02, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x04,
		0x01, 0x00, 0x01, 0x2f,
	}

	if _, err := conn.Write(badVerFrame); err != nil {
		t.Fatalf("failed to write frame: %v", err)
	}

	hdr, pay := readFrameRaw(t, conn)
	if hdr[0] != 0x01 { // server sends v1 response
		t.Errorf("expected version 1 in response, got %d", hdr[0])
	}
	if hdr[1] != 0x02 { // RESPONSE
		t.Errorf("expected RESPONSE frame, got 0x%02x", hdr[1])
	}
	if hdr[2] != 0x01 { // END_STREAM
		t.Errorf("expected END_STREAM on framing error, got 0x%02x", hdr[2])
	}
	reqID := binary.BigEndian.Uint32(hdr[4:8])
	if reqID != 0 {
		t.Errorf("expected request_id 0 on framing error, got %d", reqID)
	}
	status := binary.BigEndian.Uint16(pay[:2])
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}

	// The connection MUST be closed by the server
	buf := make([]byte, 10)
	n, err := conn.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF (closed connection), got n=%d, err=%v", n, err)
	}
}

// Test Flag: --inject-unknown sends 0x7F frame before every RESPONSE
func TestInjectUnknownFlag(t *testing.T) {
	tempDir := t.TempDir()
	indexFile := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(indexFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	absRoot, _ := filepath.Abs(tempDir)
	srv := &Server{
		RootDir:       tempDir,
		AbsRootDir:    filepath.Clean(absRoot),
		Port:          "",
		InjectUnknown: true, // test flag
		Verbose:       false,
		quit:          make(chan struct{}),
	}
	go func() {
		_ = srv.Serve(l)
	}()
	defer srv.Close()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Send valid REQUEST
	req := []byte{
		0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x0f,
		0x01,                   // GET
		0x00, 0x0b,             // path_len=11
		0x2f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x2e, 0x68, 0x74, 0x6d, 0x6c,
		0x00,                   // 0 headers
	}
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("failed to write req: %v", err)
	}

	// 1. MUST receive the 0x7F frame first!
	unkHdr, unkPay := readFrameRaw(t, conn)
	if unkHdr[1] != 0x7f {
		t.Fatalf("expected injected frame type 0x7F, got 0x%02x", unkHdr[1])
	}
	if len(unkPay) != 8 {
		t.Fatalf("expected 8 bytes junk payload, got %d", len(unkPay))
	}
	if binary.BigEndian.Uint32(unkHdr[4:8]) != 1 {
		t.Fatalf("expected request_id 1 on injected frame, got %d", binary.BigEndian.Uint32(unkHdr[4:8]))
	}

	// 2. Next, MUST receive the 200 RESPONSE frame
	respHdr, respPay := readFrameRaw(t, conn)
	if respHdr[1] != 0x02 {
		t.Fatalf("expected RESPONSE frame, got 0x%02x", respHdr[1])
	}
	status := binary.BigEndian.Uint16(respPay[:2])
	if status != 200 {
		t.Fatalf("expected status 200, got %d", status)
	}

	// 3. Receive DATA frame
	dataHdr, dataPay := readFrameRaw(t, conn)
	if dataHdr[1] != 0x03 {
		t.Fatalf("expected DATA frame, got 0x%02x", dataHdr[1])
	}
	if !bytes.Equal(dataPay, []byte("test content")) {
		t.Fatalf("unexpected body: %q", string(dataPay))
	}
}

