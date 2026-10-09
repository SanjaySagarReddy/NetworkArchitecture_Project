package proto

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// StaticTable contains the 10 predefined header names (1-indexed).
var StaticTable = [...]string{
	"",              // 0: unused
	"content-type",   // 1
	"content-length", // 2
	"host",           // 3
	"user-agent",     // 4
	"accept",         // 5
	"server",         // 6
	"date",           // 7
	"last-modified",  // 8
	"connection",     // 9
	"cache-control",  // 10
}

// StaticTableIndex maps lowercase header names to their 1-based index.
var StaticTableIndex = map[string]uint8{
	"content-type":   1,
	"content-length": 2,
	"host":           3,
	"user-agent":     4,
	"accept":         5,
	"server":         6,
	"date":           7,
	"last-modified":  8,
	"connection":     9,
	"cache-control":  10,
}

// HeaderEntry represents a key-value header entry.
type HeaderEntry struct {
	Name  string
	Value string
}

// EncodeHeaderBlock encodes a list of headers into BHTTP/1 header block format.
// Senders MUST use the indexed form for names in the static table.
func EncodeHeaderBlock(headers []HeaderEntry) ([]byte, error) {
	if len(headers) > 255 {
		return nil, fmt.Errorf("too many headers: %d (max 255)", len(headers))
	}
	buf := make([]byte, 0, 64)
	buf = append(buf, uint8(len(headers)))

	for _, h := range headers {
		lowerName := strings.ToLower(h.Name)
		valBytes := []byte(h.Value)
		if len(valBytes) > 65535 {
			return nil, fmt.Errorf("header value too long: %d bytes (max 65535)", len(valBytes))
		}

		if idx, ok := StaticTableIndex[lowerName]; ok {
			// Indexed name: 1 byte (0x80 | index), u16 value_len, value bytes
			buf = append(buf, 0x80|idx)
			var lenBuf [2]byte
			binary.BigEndian.PutUint16(lenBuf[:], uint16(len(valBytes)))
			buf = append(buf, lenBuf[:]...)
			buf = append(buf, valBytes...)
		} else {
			// Literal name: 1 byte 0x00, u8 name_len, name bytes, u16 value_len, value bytes
			nameBytes := []byte(lowerName)
			if len(nameBytes) > 255 {
				return nil, fmt.Errorf("literal header name too long: %d (max 255)", len(nameBytes))
			}
			buf = append(buf, 0x00)
			buf = append(buf, uint8(len(nameBytes)))
			buf = append(buf, nameBytes...)
			var lenBuf [2]byte
			binary.BigEndian.PutUint16(lenBuf[:], uint16(len(valBytes)))
			buf = append(buf, lenBuf[:]...)
			buf = append(buf, valBytes...)
		}
	}

	return buf, nil
}

// DecodeHeaderBlock parses a BHTTP/1 header block from data.
// Returns the parsed headers, the number of bytes consumed, or an error.
func DecodeHeaderBlock(data []byte) ([]HeaderEntry, int, error) {
	if len(data) < 1 {
		return nil, 0, fmt.Errorf("header block truncated: missing count")
	}

	count := int(data[0])
	offset := 1
	entries := make([]HeaderEntry, 0, count)

	for i := 0; i < count; i++ {
		if offset >= len(data) {
			return nil, 0, fmt.Errorf("header block truncated at entry %d of %d", i+1, count)
		}

		firstByte := data[offset]
		offset++

		if (firstByte & 0x80) == 0x80 {
			// Indexed name
			idx := firstByte & 0x7F
			if idx < 1 || int(idx) >= len(StaticTable) {
				return nil, 0, fmt.Errorf("invalid static table index %d", idx)
			}
			name := StaticTable[idx]

			if offset+2 > len(data) {
				return nil, 0, fmt.Errorf("truncated value_len in indexed header")
			}
			valLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2

			if offset+valLen > len(data) {
				return nil, 0, fmt.Errorf("truncated value bytes in indexed header")
			}
			val := string(data[offset : offset+valLen])
			offset += valLen

			entries = append(entries, HeaderEntry{Name: name, Value: val})
		} else if firstByte == 0x00 {
			// Literal name
			if offset >= len(data) {
				return nil, 0, fmt.Errorf("truncated name_len in literal header")
			}
			nameLen := int(data[offset])
			offset++
			if nameLen == 0 {
				return nil, 0, fmt.Errorf("empty name in literal header")
			}

			if offset+nameLen > len(data) {
				return nil, 0, fmt.Errorf("truncated name bytes in literal header")
			}
			nameBytes := data[offset : offset+nameLen]
			offset += nameLen

			for _, b := range nameBytes {
				if b < 33 || b > 126 || (b >= 'A' && b <= 'Z') {
					return nil, 0, fmt.Errorf("invalid character in literal header name: 0x%02x", b)
				}
			}
			name := string(nameBytes)

			if offset+2 > len(data) {
				return nil, 0, fmt.Errorf("truncated value_len in literal header")
			}
			valLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2

			if offset+valLen > len(data) {
				return nil, 0, fmt.Errorf("truncated value bytes in literal header")
			}
			val := string(data[offset : offset+valLen])
			offset += valLen

			entries = append(entries, HeaderEntry{Name: name, Value: val})
		} else {
			return nil, 0, fmt.Errorf("malformed header entry first byte: 0x%02x", firstByte)
		}
	}

	return entries, offset, nil
}
