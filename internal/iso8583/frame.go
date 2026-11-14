package iso8583

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ReadFrame and WriteFrame implement jPOS PostChannel's two-byte big-endian length prefix.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(header[:]))
	if length == 0 {
		return nil, fmt.Errorf("zero-length ISO frame")
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}
	return frame, nil
}
func WriteFrame(w io.Writer, frame []byte) error {
	if len(frame) == 0 || len(frame) > 65535 {
		return fmt.Errorf("invalid ISO frame length %d", len(frame))
	}
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(frame)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(frame)
	return err
}
