package connector

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

func TestClient_StartsPersistentConnectionAndCompletesHandshake(t *testing.T) {
	packager, err := iso8583.LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	handshakeComplete := make(chan struct{}, 1)
	connectionClosed := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		for _, networkCode := range []string{"001", "301"} {
			request := iso8583.Message{Fields: map[int][]byte{
				0: []byte("0800"), 7: []byte("0101123456"), 11: []byte("000001"), 33: []byte("886"), 70: []byte(networkCode),
			}}
			wire, _ := packager.Pack(request)
			if iso8583.WriteFrame(conn, wire) != nil {
				return
			}
		}
		for range 2 {
			frame, readErr := iso8583.ReadFrame(conn)
			if readErr != nil {
				return
			}
			response, unpackErr := packager.Unpack(frame)
			if unpackErr != nil || string(response.Fields[0]) != "0810" || string(response.Fields[39]) != "00" {
				return
			}
		}
		handshakeComplete <- struct{}{}
		frame, readErr := iso8583.ReadFrame(conn)
		if readErr != nil {
			return
		}
		businessRequest, unpackErr := packager.Unpack(frame)
		if unpackErr != nil || string(businessRequest.Fields[0]) != "0200" {
			return
		}
		response := responseTo(businessRequest, "0210", "00")
		wire, _ := packager.Pack(response)
		if iso8583.WriteFrame(conn, wire) != nil {
			return
		}
		buffer := make([]byte, 1)
		_, _ = conn.Read(buffer)
		connectionClosed <- struct{}{}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClient(ClientConfig{Address: listener.Addr().String(), Timeout: time.Second, HandshakeTimeout: 20 * time.Millisecond, Packager: packager})
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-handshakeComplete:
	case <-time.After(time.Second):
		t.Fatal("persistent client did not complete initial handshake")
	}
	response, callErr := client.Call(context.Background(), iso8583.Message{Fields: map[int][]byte{
		0: []byte("0200"), 3: []byte("340000"), 4: []byte("000000001000"), 7: []byte("0101123456"),
		11: []byte("000123"), 37: []byte("ABC123DEF456"), 41: []byte("TERM0001"), 49: []byte("IDR"), 104: []byte("XXX1231234567890"),
	}})
	if callErr != nil {
		t.Fatalf("Call() over persistent connection error = %v", callErr)
	}
	if got, want := string(response.Fields[0]), "0210"; got != want {
		t.Fatalf("business response MTI = %q, want %q", got, want)
	}
	select {
	case <-connectionClosed:
		t.Fatal("client closed TCP connection immediately after initial handshake")
	case <-time.After(50 * time.Millisecond):
	}
}
