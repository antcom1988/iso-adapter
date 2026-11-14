package iso8583

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestPack_ClientSpecPadsShortTerminalID(t *testing.T) {
	packager, err := LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	message := Message{Fields: map[int][]byte{
		0: []byte("0200"), 3: []byte("340000"), 4: []byte("000000001000"), 7: []byte("0101123456"),
		11: []byte("000123"), 37: []byte("123456789012"), 41: []byte("TERM0001"), 49: []byte("IDR"), 104: []byte("XXX1231234567890"),
	}}
	wire, err := packager.Pack(message)
	if err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	if !bytes.Contains(wire, []byte("TERM0001        IDR16XXX1231234567890")) {
		t.Fatalf("wire does not contain the padded terminal ID: %q", wire)
	}
	unpacked, err := packager.Unpack(wire)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}
	if got, want := string(unpacked.Fields[41]), "TERM0001        "; got != want {
		t.Errorf("field 41 = %q, want %q", got, want)
	}
}
