package connector

import (
	"testing"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

func TestResponseToNetwork_MatchesRequiredFields(t *testing.T) {
	request := iso8583.Message{Fields: map[int][]byte{
		0:  []byte("0800"),
		7:  []byte("0101123456"),
		11: []byte("000101"),
		15: []byte("0101"),
		33: []byte("886"),
		48: []byte("FORMAL-LOGON"),
		70: []byte("201"),
	}}

	response := responseToNetwork(request, "00")
	for field, want := range map[int]string{0: "0810", 7: "0101123456", 11: "000101", 15: "0101", 33: "886", 39: "00", 70: "201"} {
		if got := string(response.Fields[field]); got != want {
			t.Errorf("DE%d = %q, want %q", field, got, want)
		}
	}
	if _, present := response.Fields[48]; present {
		t.Fatal("DE48 must not be echoed in the network response")
	}
}
