# bserve — BHTTP/1 Server

`bserve` is the server implementation of the BHTTP/1 (Binary HTTP over TCP) protocol.

The full protocol specification is defined in [SPEC.md](file:///c:/Scaler%20Academics/Network%20Architecture/Final%20Project/SPEC.md).

The client half is built separately by teammate Sachith: [Network-Project (Client Repo)](https://github.com/Sachith77/Network-Project).

## Features

- **Binary Framing**: Fixed 12-byte headers with network byte order integers and 1 MiB payload bound.
- **Header Compression**: Static dictionary compression for the 10 core headers and literal support.
- **Chunked Data Streaming**: Large bodies streamed in DATA frames of at most 16,384 bytes.
- **Strict Bounds & Error Handling**: Immediate bounds checking, unknown frame tolerance, and path traversal protection.
- **Diagnostics**:
  - `-v`: Hexdump every frame sent/received (xxd-style, 16 bytes per line) with decoded header lines.
  - `--inject-unknown`: Injects 0x7F frame with 8-byte junk payload prior to every RESPONSE to test client tolerance.

## Building

Using the Go standard library only:

```bash
go build -o bserve
```

*(On Windows, build with `go build -o bserve.exe`)*

## Running

```bash
./bserve <root_dir> <port>
```

Example serving the sample `www` directory on port 9000:

```bash
./bserve ./www 9000
```

With verbose hexdump logging enabled:

```bash
./bserve -v ./www 9000
```

With unknown frame injection enabled (for client testing):

```bash
./bserve --inject-unknown -v ./www 9000
```

## Testing

Run all unit and end-to-end integration tests:

```bash
go test -v ./...
```

The test suite covers:
1. Decoding the exact byte sequence from SPEC §8 into `GET /index.html` with headers.
2. Encoding a 404 response to match the exact byte sequence from SPEC §8.
3. End-to-end sequential requests over a live TCP socket (`200 OK` followed by `404 Not Found`).
4. Unknown frame skipping (`0x7F` frame preceding a valid request).
5. Malformed/truncated request error handling (`400 Bad Request` with connection maintained).
6. Root escape prevention (`/../etc/passwd` returning `400 Bad Request`).
7. Framing error enforcement (incompatible version returning `400 Bad Request` with `id=0` and connection closed).
8. Flag verification for `--inject-unknown`.
