package connector

import (
	"bytes"
	"context"
	"log"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

func TestClient_RespondsToInitialSignOnAndEchoBeforeBusinessRequest(t *testing.T) {
	packager, err := iso8583.LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	received := make(chan []string, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		for _, networkCode := range []string{"001", "301"} {
			networkRequest := iso8583.Message{Fields: map[int][]byte{
				0: []byte("0800"), 7: []byte("0101123456"), 11: []byte("000001"), 33: []byte("886"), 70: []byte(networkCode),
			}}
			wire, _ := packager.Pack(networkRequest)
			if iso8583.WriteFrame(conn, wire) != nil {
				return
			}
		}

		mtis := make([]string, 0, 3)
		for i := 0; i < 2; i++ {
			frame, readErr := iso8583.ReadFrame(conn)
			if readErr != nil {
				received <- append(mtis, "read-error")
				return
			}
			message, unpackErr := packager.Unpack(frame)
			if unpackErr != nil {
				received <- append(mtis, "unpack-error")
				return
			}
			mtis = append(mtis, string(message.Fields[0]))
		}

		frame, readErr := iso8583.ReadFrame(conn)
		if readErr != nil {
			received <- append(mtis, "read-error")
			return
		}
		businessRequest, unpackErr := packager.Unpack(frame)
		if unpackErr != nil {
			received <- append(mtis, "unpack-error")
			return
		}
		mtis = append(mtis, string(businessRequest.Fields[0]))
		response := responseTo(businessRequest, "0210", "00")
		wire, _ := packager.Pack(response)
		_ = iso8583.WriteFrame(conn, wire)
		received <- mtis
	}()

	var logs bytes.Buffer
	client := NewClient(ClientConfig{Address: listener.Addr().String(), Timeout: time.Second, HandshakeTimeout: 20 * time.Millisecond, Packager: packager, Logger: log.New(&logs, "", 0)})
	response, callErr := client.Call(context.Background(), iso8583.Message{Fields: map[int][]byte{
		0: []byte("0200"), 3: []byte("340000"), 4: []byte("000000001000"), 7: []byte("0101123456"),
		11: []byte("000123"), 37: []byte("ABC123DEF456"), 41: []byte("TERM0001"), 49: []byte("IDR"), 104: []byte("XXX1231234567890"),
	}})
	if callErr != nil {
		t.Fatalf("Call() error = %v", callErr)
	}
	if got, want := string(response.Fields[0]), "0210"; got != want {
		t.Fatalf("response MTI = %q, want %q", got, want)
	}
	for _, want := range []string{"direction=incoming", "direction=outgoing"} {
		if !bytes.Contains(logs.Bytes(), []byte(want)) {
			t.Fatalf("network handshake log = %q, want %q", logs.String(), want)
		}
	}

	select {
	case got := <-received:
		want := []string{"0810", "0810", "0200"}
		if len(got) != len(want) {
			t.Fatalf("received MTIs = %#v, want %#v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("received MTIs = %#v, want %#v", got, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive handshake and business request")
	}
}
