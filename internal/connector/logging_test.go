package connector

import (
	"testing"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

func TestLogFields_RedactsSensitiveISOData(t *testing.T) {
	fields := LogFields(iso8583.Message{Fields: map[int][]byte{
		0:  []byte("0200"),
		2:  []byte("4111111111111111"),
		3:  []byte("340000"),
		37: []byte("ABC123DEF456"),
		52: []byte("0123456789ABCDEF"),
	}})

	if got, want := fields["0"], "0200"; got != want {
		t.Errorf("field 0 = %q, want %q", got, want)
	}
	if got, want := fields["37"], "ABC123DEF456"; got != want {
		t.Errorf("field 37 = %q, want %q", got, want)
	}
	for _, field := range []string{"2", "52"} {
		if got, want := fields[field], "***REDACTED***"; got != want {
			t.Errorf("field %s = %q, want %q", field, got, want)
		}
	}
}
