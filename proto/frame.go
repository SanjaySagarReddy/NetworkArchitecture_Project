package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const (
	// Version is the protocol version for BHTTP/1.
	Version uint8 = 0x01

	// Frame Types
	FrameTypeRequest  uint8 = 0x01
	FrameTypeResponse uint8 = 0x02
	FrameTypeData     uint8 = 0x03

	// Flags
	FlagEndStream uint8 = 0x01

	// Limits
	MaxPayloadLength uint32 = 1048576 // 1 MiB (1,048,576 bytes)
	MaxDataChunkSize int    = 16384   // 16 KiB
	HeaderSize       int    = 12      // Fixed 12-byte frame header
)

// Header represents the 12-byte fixed frame header.
type Header struct {
	Version   uint8
	Type      uint8
	Flags     uint8
	Reserved  uint8
	RequestID uint32
	Length    uint32
}

// Frame represents a complete frame (header + payload).
type Frame struct {
	Header  Header
	Payload []byte
}

// ReadHeader reads exactly 12 bytes from r and parses the frame header.
func ReadHeader(r io.Reader) (Header, error) {
	var buf [HeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return Header{}, err
	}
	return Header{
		Version:   buf[0],
		Type:      buf[1],
		Flags:     buf[2],
		Reserved:  buf[3],
		RequestID: binary.BigEndian.Uint32(buf[4:8]),
		Length:    binary.BigEndian.Uint32(buf[8:12]),
	}, nil
}

// Encode serializes the Header into 12 bytes.
func (h Header) Encode() []byte {
	buf := make([]byte, HeaderSize)
	buf[0] = h.Version
	buf[1] = h.Type
	buf[2] = h.Flags
	buf[3] = h.Reserved
	binary.BigEndian.PutUint32(buf[4:8], h.RequestID)
	binary.BigEndian.PutUint32(buf[8:12], h.Length)
	return buf
}

// Encode serializes the entire Frame into bytes.
func (f *Frame) Encode() []byte {
	f.Header.Length = uint32(len(f.Payload))
	hBytes := f.Header.Encode()
	res := make([]byte, HeaderSize+len(f.Payload))
	copy(res[:HeaderSize], hBytes)
	copy(res[HeaderSize:], f.Payload)
	return res
}

// WriteTo writes the frame to w.
func (f *Frame) WriteTo(w io.Writer) error {
	b := f.Encode()
	_, err := w.Write(b)
	return err
}

// TypeToString returns human-readable representation of a frame type.
func TypeToString(t uint8) string {
	switch t {
	case FrameTypeRequest:
		return "REQUEST"
	case FrameTypeResponse:
		return "RESPONSE"
	case FrameTypeData:
		return "DATA"
	default:
		return fmt.Sprintf("0x%02x", t)
	}
}

// FlagsToString returns human-readable representation of flags.
func FlagsToString(f uint8) string {
	if f == 0 {
		return "0"
	}
	if f == FlagEndStream {
		return "END_STREAM"
	}
	if (f & FlagEndStream) != 0 {
		return fmt.Sprintf("END_STREAM|0x%02x", f & ^FlagEndStream)
	}
	return fmt.Sprintf("0x%02x", f)
}

// DumpFrame writes an xxd-style hexdump of a frame with prefix to w.
func DumpFrame(w io.Writer, prefix string, h Header, payload []byte) {
	// Decoded header line above dump
	fmt.Fprintf(w, "%sver=%d type=%s flags=%s rsv=%d id=%d len=%d\n",
		prefix, h.Version, TypeToString(h.Type), FlagsToString(h.Flags), h.Reserved, h.RequestID, h.Length)

	full := make([]byte, HeaderSize+len(payload))
	copy(full[:HeaderSize], h.Encode())
	copy(full[HeaderSize:], payload)

	for offset := 0; offset < len(full); offset += 16 {
		end := offset + 16
		if end > len(full) {
			end = len(full)
		}
		lineBytes := full[offset:end]

		var hexPart strings.Builder
		for i := 0; i < 16; i++ {
			if i < len(lineBytes) {
				fmt.Fprintf(&hexPart, "%02x ", lineBytes[i])
			} else {
				hexPart.WriteString("   ")
			}
		}

		var asciiPart strings.Builder
		for _, b := range lineBytes {
			if b >= 32 && b <= 126 {
				asciiPart.WriteByte(b)
			} else {
				asciiPart.WriteByte('.')
			}
		}

		fmt.Fprintf(w, "%s%08x: %s %s\n", prefix, offset, hexPart.String(), asciiPart.String())
	}
}
