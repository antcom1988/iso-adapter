package iso8583

import (
	"path/filepath"
	"testing"
)

func TestPackUnpack_RoundTripsInquiry(t *testing.T) {
	p, err := LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	original := Message{Fields: map[int][]byte{
		0:   []byte("0200"),
		3:   []byte("340000"),
		4:   []byte("000000001000"),
		7:   []byte("0101123456"),
		11:  []byte("000123"),
		37:  []byte("ABC123DEF456"),
		41:  []byte("TERM0001        "),
		49:  []byte("IDR"),
		104: []byte("XXX1231234567890"),
	}}

	wire, err := p.Pack(original)
	if err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	got, err := p.Unpack(wire)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}
	for field, want := range original.Fields {
		if string(got.Fields[field]) != string(want) {
			t.Errorf("field %d = %q, want %q", field, got.Fields[field], want)
		}
	}
}

func TestPack_RejectsOversizedVariableField(t *testing.T) {
	p, err := LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	_, err = p.Pack(Message{Fields: map[int][]byte{0: []byte("0200"), 2: []byte("12345678901234567890")}})
	if err == nil {
		t.Fatal("Pack() error = nil, want length validation error")
	}
}
