package iso8583

import (
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Message struct{ Fields map[int][]byte }

type fieldDefinition struct {
	ID     int    `xml:"id,attr"`
	Length int    `xml:"length,attr"`
	Class  string `xml:"class,attr"`
}
type packagerDocument struct {
	Fields []fieldDefinition `xml:"isofield"`
}
type Packager struct{ fields map[int]fieldDefinition }

func LoadPackager(path string) (*Packager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read packager: %w", err)
	}
	var doc packagerDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse packager XML: %w", err)
	}
	p := &Packager{fields: make(map[int]fieldDefinition, len(doc.Fields))}
	for _, f := range doc.Fields {
		if f.ID < 0 || f.ID > 128 || f.Length <= 0 {
			return nil, fmt.Errorf("invalid definition for field %d", f.ID)
		}
		p.fields[f.ID] = f
	}
	if _, ok := p.fields[0]; !ok {
		return nil, fmt.Errorf("packager has no MTI field")
	}
	return p, nil
}

func (p *Packager) Pack(m Message) ([]byte, error) {
	if m.Fields == nil || len(m.Fields[0]) != 4 {
		return nil, fmt.Errorf("field 0 (MTI) must contain exactly 4 bytes")
	}
	var fields []int
	for n := range m.Fields {
		if n > 1 {
			fields = append(fields, n)
		}
	}
	sort.Ints(fields)
	bitmap, err := makeBitmap(fields)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 128)
	out = append(out, m.Fields[0]...)
	out = append(out, []byte(strings.ToUpper(hex.EncodeToString(bitmap)))...)
	for _, n := range fields {
		f, ok := p.fields[n]
		if !ok {
			return nil, fmt.Errorf("field %d is absent from packager", n)
		}
		encoded, err := encodeField(f, m.Fields[n])
		if err != nil {
			return nil, fmt.Errorf("field %d: %w", n, err)
		}
		out = append(out, encoded...)
	}
	return out, nil
}

func (p *Packager) Unpack(wire []byte) (Message, error) {
	if len(wire) < 20 {
		return Message{}, fmt.Errorf("ISO message shorter than MTI and primary bitmap")
	}
	m := Message{Fields: map[int][]byte{0: append([]byte(nil), wire[:4]...)}}
	offset := 4
	primary, err := decodeBitmap(wire[offset : offset+16])
	if err != nil {
		return Message{}, err
	}
	offset += 16
	bitmaps := [][]byte{primary}
	if primary[0]&0x80 != 0 {
		if len(wire) < offset+16 {
			return Message{}, fmt.Errorf("missing secondary bitmap")
		}
		secondary, err := decodeBitmap(wire[offset : offset+16])
		if err != nil {
			return Message{}, err
		}
		offset += 16
		bitmaps = append(bitmaps, secondary)
	}
	for i, bitmap := range bitmaps {
		for bit := 1; bit <= 64; bit++ {
			if bitmap[(bit-1)/8]&(1<<uint(7-(bit-1)%8)) == 0 {
				continue
			}
			n := i*64 + bit
			if n == 1 {
				continue
			}
			f, ok := p.fields[n]
			if !ok {
				return Message{}, fmt.Errorf("field %d is present but undefined", n)
			}
			value, next, err := decodeField(f, wire, offset)
			if err != nil {
				return Message{}, fmt.Errorf("field %d: %w", n, err)
			}
			m.Fields[n] = value
			offset = next
		}
	}
	if offset != len(wire) {
		return Message{}, fmt.Errorf("trailing data: consumed %d of %d bytes", offset, len(wire))
	}
	return m, nil
}

func makeBitmap(fields []int) ([]byte, error) {
	secondary := false
	for _, n := range fields {
		if n < 2 || n > 128 {
			return nil, fmt.Errorf("field number %d out of range", n)
		}
		secondary = secondary || n > 64
	}
	size := 8
	if secondary {
		size = 16
	}
	bitmap := make([]byte, size)
	if secondary {
		bitmap[0] = 0x80
	}
	for _, n := range fields {
		index := n - 1
		bitmap[index/8] |= 1 << uint(7-index%8)
	}
	return bitmap, nil
}
func decodeBitmap(data []byte) ([]byte, error) {
	decoded := make([]byte, 8)
	if _, err := hex.Decode(decoded, data); err != nil {
		return nil, fmt.Errorf("invalid ASCII-hex bitmap: %w", err)
	}
	return decoded, nil
}
func variableDigits(class string) int {
	if strings.Contains(class, "LLL") {
		return 3
	}
	if strings.Contains(class, "LL") {
		return 2
	}
	return 0
}
func isASCIIBinary(class string) bool {
	return strings.Contains(class, "IFA_BINARY") || strings.Contains(class, "IFA_LLBINARY")
}
func encodeField(f fieldDefinition, value []byte) ([]byte, error) {
	logical := value
	if isASCIIBinary(f.Class) {
		if _, err := hex.DecodeString(string(value)); err != nil {
			return nil, fmt.Errorf("binary field requires hexadecimal text: %w", err)
		}
		logical = value
		if len(value)%2 != 0 {
			return nil, fmt.Errorf("binary field hexadecimal text has odd length")
		}
	}
	actualLength := len(logical)
	if isASCIIBinary(f.Class) {
		actualLength /= 2
	}
	digits := variableDigits(f.Class)
	if digits > 0 {
		if actualLength > f.Length {
			return nil, fmt.Errorf("length %d exceeds maximum %d", actualLength, f.Length)
		}
		return append([]byte(fmt.Sprintf("%0*d", digits, actualLength)), logical...), nil
	}
	if actualLength != f.Length {
		if strings.Contains(f.Class, "IF_CHAR") && actualLength < f.Length {
			padded := make([]byte, f.Length)
			copy(padded, logical)
			for i := actualLength; i < f.Length; i++ {
				padded[i] = ' '
			}
			return padded, nil
		}
		return nil, fmt.Errorf("length %d, want %d", actualLength, f.Length)
	}
	return append([]byte(nil), logical...), nil
}
func decodeField(f fieldDefinition, wire []byte, offset int) ([]byte, int, error) {
	logicalLength := f.Length
	digits := variableDigits(f.Class)
	if digits > 0 {
		if len(wire) < offset+digits {
			return nil, offset, fmt.Errorf("missing length prefix")
		}
		n, err := strconv.Atoi(string(wire[offset : offset+digits]))
		if err != nil {
			return nil, offset, fmt.Errorf("invalid length prefix: %w", err)
		}
		if n > f.Length {
			return nil, offset, fmt.Errorf("declared length %d exceeds maximum %d", n, f.Length)
		}
		logicalLength = n
		offset += digits
	}
	wireLength := logicalLength
	if isASCIIBinary(f.Class) {
		wireLength *= 2
	}
	if len(wire) < offset+wireLength {
		return nil, offset, fmt.Errorf("truncated field: need %d bytes", wireLength)
	}
	return append([]byte(nil), wire[offset:offset+wireLength]...), offset + wireLength, nil
}
