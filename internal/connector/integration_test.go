package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

func TestClientAndServer_ProcessInquiryOverTCP(t *testing.T) {
	packager, err := iso8583.LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	received := make(chan map[string]string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MESSAGE_URL request: %v", err)
		}
		received <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"0":"0210","3":"340000","11":"000123","37":"ABC123DEF456","38":"ABC123","39":"00"}`))
	}))
	defer upstream.Close()

	server, err := NewServer(ServerConfig{ListenAddr: "127.0.0.1:0", MessageURL: upstream.URL, Packager: packager})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Serve(ctx)
	defer server.Close()

	client := NewClient(ClientConfig{Address: server.Addr(), Timeout: time.Second, Packager: packager})
	requestFields := map[int][]byte{
		0: []byte("0200"), 2: []byte("1234567890123456"), 3: []byte("340000"), 4: []byte("000000001000"),
		7: []byte("1113214500"), 11: []byte("000123"), 12: []byte("214500"), 13: []byte("1113"),
		15: []byte("1113"), 17: []byte("1113"), 18: []byte("6012"), 32: []byte("886"), 33: []byte("886"),
		37: []byte("ABC123DEF456"), 41: []byte("TERM0001"), 42: []byte("MERCHANT0000001"),
		43: []byte("SIMULATOR MERCHANT JAKARTA              "), 49: []byte("IDR"), 104: []byte("XXX1231234567890"),
	}
	response, err := client.Call(context.Background(), iso8583.Message{Fields: requestFields})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got := string(response.Fields[0]); got != "0210" {
		t.Errorf("response MTI = %q, want 0210", got)
	}
	if got := string(response.Fields[39]); got != "00" {
		t.Errorf("response code = %q, want 00", got)
	}
	gotRequest := <-received
	wantRequest := map[string]string{}
	for field, value := range requestFields {
		wantRequest[strconv.Itoa(field)] = string(value)
	}
	// Fixed-width DE41 is padded by the packager before the server receives it.
	wantRequest["41"] = "TERM0001        "
	if !reflect.DeepEqual(gotRequest, wantRequest) {
		t.Errorf("MESSAGE_URL request = %#v, want ISO field map %#v", gotRequest, wantRequest)
	}
}

func TestHTTPHandler_ProxiesJSONCallToISO(t *testing.T) {
	packager, err := iso8583.LoadPackager(filepath.Join("..", "..", "cfg", "packager", "iso_spec.xml"))
	if err != nil {
		t.Fatalf("LoadPackager() error = %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"0":"0210","3":"340000","11":"000123","37":"ABC123DEF456","39":"00"}`))
	}))
	defer upstream.Close()
	isoServer, err := NewServer(ServerConfig{ListenAddr: "127.0.0.1:0", MessageURL: upstream.URL, Packager: packager})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go isoServer.Serve(ctx)
	defer isoServer.Close()

	httpServer := httptest.NewServer(HTTPHandler(NewClient(ClientConfig{Address: isoServer.Addr(), Timeout: time.Second, Packager: packager})))
	defer httpServer.Close()
	payload := []byte(`{"0":"0200","3":"340000","4":"000000001000","7":"0101123456","11":"000123","37":"ABC123DEF456","41":"TERM0001","49":"IDR","104":"XXX1231234567890"}`)
	response, err := http.Post(httpServer.URL+"/call", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /call error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST /call status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["0"] != "0210" || body["39"] != "00" {
		t.Fatalf("response = %#v, want MTI 0210 and response code 00", body)
	}
}
