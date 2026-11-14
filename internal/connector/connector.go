package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

type ClientConfig struct {
	Address          string
	Timeout          time.Duration
	HandshakeTimeout time.Duration
	Packager         *iso8583.Packager
	Logger           *log.Logger
}
type Client struct {
	config ClientConfig
	logger *log.Logger

	mu         sync.Mutex
	writeMu    sync.Mutex
	callMu     sync.Mutex
	persistent net.Conn
	responses  chan iso8583.Message
	done       chan struct{}
	started    bool
}

func NewClient(config ClientConfig) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 250 * time.Millisecond
	}
	return &Client{config: config, logger: loggerOrDefault(config.Logger)}
}

// Start opens the long-lived TCP session used by the client role and completes
// the peer's initial network-management exchange before accepting business traffic.
func (c *Client) Start(ctx context.Context) error {
	if c.config.Packager == nil || c.config.Address == "" {
		return fmt.Errorf("client packager and address are required")
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	dialer := net.Dialer{Timeout: c.config.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.config.Address)
	if err != nil {
		c.logger.Printf("event=tcp_connect component=client peer=%s status=error error=%q", c.config.Address, err)
		return err
	}
	c.logger.Printf("event=tcp_connect component=client peer=%s status=connected", c.config.Address)
	if err := c.initialNetworkHandshake(conn); err != nil {
		_ = conn.Close()
		c.logger.Printf("event=network_handshake component=client peer=%s status=error error=%q", c.config.Address, err)
		return err
	}

	c.mu.Lock()
	c.persistent = conn
	c.responses = make(chan iso8583.Message, 1)
	c.done = make(chan struct{})
	c.started = true
	c.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	go c.readPersistentNetwork(conn)
	return nil
}

func (c *Client) readPersistentNetwork(conn net.Conn) {
	defer func() {
		c.mu.Lock()
		if c.persistent == conn && c.done != nil {
			close(c.done)
			c.persistent = nil
		}
		c.mu.Unlock()
	}()
	for {
		frame, err := iso8583.ReadFrame(conn)
		if err != nil {
			c.logger.Printf("event=tcp_connect component=client peer=%s status=closed error=%q", c.config.Address, err)
			return
		}
		message, err := c.config.Packager.Unpack(frame)
		if err != nil {
			c.logger.Printf("event=iso component=client direction=incoming peer=%s status=unpack_error error=%q", c.config.Address, err)
			return
		}
		if string(message.Fields[0]) != "0800" {
			logISO(c.logger, "client", "response", c.config.Address, message)
			c.mu.Lock()
			responses := c.responses
			c.mu.Unlock()
			select {
			case responses <- message:
			default:
				c.logger.Printf("event=iso component=client direction=incoming peer=%s status=unexpected_response", c.config.Address)
			}
			continue
		}
		logISO(c.logger, "client", "incoming", c.config.Address, message)
		if err := c.replyNetwork(conn, message); err != nil {
			c.logger.Printf("event=iso component=client direction=outgoing peer=%s status=write_error error=%q", c.config.Address, err)
			return
		}
	}
}

func (c *Client) Call(ctx context.Context, request iso8583.Message) (iso8583.Message, error) {
	c.mu.Lock()
	persistent, responses, done := c.persistent, c.responses, c.done
	c.mu.Unlock()
	if persistent != nil {
		return c.callPersistent(ctx, persistent, responses, done, request)
	}
	if c.config.Packager == nil || c.config.Address == "" {
		return iso8583.Message{}, fmt.Errorf("client packager and address are required")
	}
	dialer := net.Dialer{Timeout: c.config.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.config.Address)
	if err != nil {
		c.logger.Printf("event=tcp_connect component=client peer=%s status=error error=%q", c.config.Address, err)
		return iso8583.Message{}, err
	}
	defer conn.Close()
	c.logger.Printf("event=tcp_connect component=client peer=%s status=connected", c.config.Address)
	if err := c.initialNetworkHandshake(conn); err != nil {
		c.logger.Printf("event=network_handshake component=client peer=%s status=error error=%q", c.config.Address, err)
		return iso8583.Message{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.config.Timeout))
	}
	wire, err := c.config.Packager.Pack(request)
	if err != nil {
		return iso8583.Message{}, err
	}
	if err := iso8583.WriteFrame(conn, wire); err != nil {
		c.logger.Printf("event=iso component=client direction=request peer=%s status=write_error error=%q", c.config.Address, err)
		return iso8583.Message{}, err
	}
	logISO(c.logger, "client", "request", c.config.Address, request)
	for {
		frame, err := iso8583.ReadFrame(conn)
		if err != nil {
			c.logger.Printf("event=iso component=client direction=response peer=%s status=read_error error=%q", c.config.Address, err)
			return iso8583.Message{}, err
		}
		response, err := c.config.Packager.Unpack(frame)
		if err != nil {
			c.logger.Printf("event=iso component=client direction=response peer=%s status=unpack_error error=%q", c.config.Address, err)
			return iso8583.Message{}, err
		}
		if string(response.Fields[0]) == "0800" {
			logISO(c.logger, "client", "incoming", c.config.Address, response)
			if err := c.replyNetwork(conn, response); err != nil {
				return iso8583.Message{}, err
			}
			continue
		}
		logISO(c.logger, "client", "response", c.config.Address, response)
		return response, nil
	}
}
func (c *Client) callPersistent(ctx context.Context, conn net.Conn, responses <-chan iso8583.Message, done <-chan struct{}, request iso8583.Message) (iso8583.Message, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	wire, err := c.config.Packager.Pack(request)
	if err != nil {
		return iso8583.Message{}, err
	}
	c.writeMu.Lock()
	err = iso8583.WriteFrame(conn, wire)
	c.writeMu.Unlock()
	if err != nil {
		c.logger.Printf("event=iso component=client direction=request peer=%s status=write_error error=%q", c.config.Address, err)
		return iso8583.Message{}, err
	}
	logISO(c.logger, "client", "request", c.config.Address, request)
	select {
	case response := <-responses:
		return response, nil
	case <-done:
		return iso8583.Message{}, fmt.Errorf("persistent TCP connection closed")
	case <-ctx.Done():
		return iso8583.Message{}, ctx.Err()
	case <-time.After(c.config.Timeout):
		return iso8583.Message{}, fmt.Errorf("ISO response timeout")
	}
}

func (c *Client) initialNetworkHandshake(conn net.Conn) error {
	defer conn.SetReadDeadline(time.Time{})
	handled := 0
	for {
		_ = conn.SetReadDeadline(time.Now().Add(c.config.HandshakeTimeout))
		frame, err := iso8583.ReadFrame(conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if handled == 0 {
					c.logger.Printf("event=network_handshake component=client peer=%s status=not_required", c.config.Address)
				} else {
					c.logger.Printf("event=network_handshake component=client peer=%s status=completed network_messages=%d", c.config.Address, handled)
				}
				return nil
			}
			return err
		}
		request, err := c.config.Packager.Unpack(frame)
		if err != nil {
			return err
		}
		if string(request.Fields[0]) != "0800" {
			return fmt.Errorf("unexpected initial ISO MTI %q", request.Fields[0])
		}
		logISO(c.logger, "client", "incoming", c.config.Address, request)
		if err := c.replyNetwork(conn, request); err != nil {
			return err
		}
		handled++
	}
}

func (c *Client) replyNetwork(conn net.Conn, request iso8583.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	response := responseToNetwork(request, "00")
	wire, err := c.config.Packager.Pack(response)
	if err != nil {
		return err
	}
	if err := iso8583.WriteFrame(conn, wire); err != nil {
		return err
	}
	logISO(c.logger, "client", "outgoing", conn.RemoteAddr().String(), response)
	return nil
}

type ServerConfig struct {
	ListenAddr, MessageURL string
	Packager               *iso8583.Packager
	HTTPClient             *http.Client
	Logger                 *log.Logger
}
type Server struct {
	listener  net.Listener
	config    ServerConfig
	closeOnce sync.Once
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Packager == nil {
		return nil, fmt.Errorf("server packager is required")
	}
	if config.ListenAddr == "" {
		config.ListenAddr = ":8989"
	}
	l, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		return nil, err
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	config.Logger = loggerOrDefault(config.Logger)
	return &Server{listener: l, config: config}, nil
}
func (s *Server) Addr() string { return s.listener.Addr().String() }
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.listener.Close() })
	return err
}
func (s *Server) Serve(ctx context.Context) error {
	go func() { <-ctx.Done(); _ = s.Close() }()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			return err
		}
		go s.serveConnection(ctx, conn)
	}
}
func (s *Server) serveConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	peer := conn.RemoteAddr().String()
	s.config.Logger.Printf("event=tcp_connect component=server peer=%s status=accepted", peer)
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	for {
		frame, err := iso8583.ReadFrame(conn)
		if err != nil {
			s.config.Logger.Printf("event=iso component=server direction=request peer=%s status=read_closed error=%q", peer, err)
			return
		}
		request, err := s.config.Packager.Unpack(frame)
		if err != nil {
			s.config.Logger.Printf("event=iso component=server direction=request peer=%s status=unpack_error error=%q", peer, err)
			return
		}
		logISO(s.config.Logger, "server", "request", peer, request)
		response := s.handle(ctx, request)
		wire, err := s.config.Packager.Pack(response)
		if err != nil {
			s.config.Logger.Printf("event=iso component=server direction=response peer=%s status=pack_error error=%q", peer, err)
			return
		}
		if err := iso8583.WriteFrame(conn, wire); err != nil {
			s.config.Logger.Printf("event=iso component=server direction=response peer=%s status=write_error error=%q", peer, err)
			return
		}
		logISO(s.config.Logger, "server", "response", peer, response)
	}
}
func (s *Server) handle(ctx context.Context, request iso8583.Message) iso8583.Message {
	mti := string(request.Fields[0])
	if mti == "0800" {
		return responseToNetwork(request, "00")
	}
	if mti != "0200" {
		return responseTo(request, responseMTI(mti), "12")
	}
	response, fallbackCode := s.callUpstream(ctx, request)
	if fallbackCode != "" {
		return responseTo(request, "0210", fallbackCode)
	}
	return response
}
func (s *Server) callUpstream(ctx context.Context, request iso8583.Message) (iso8583.Message, string) {
	if s.config.MessageURL == "" {
		return iso8583.Message{}, "91"
	}
	command := ""
	processingCode := string(request.Fields[3])
	if len(processingCode) >= 2 {
		switch processingCode[:2] {
		case "34":
			command = "inquiry"
		case "40":
			command = "disbursement"
		}
	}
	if command == "" {
		return iso8583.Message{}, "12"
	}

	payload := make(map[string]string, len(request.Fields))
	for field, value := range request.Fields {
		payload[strconv.Itoa(field)] = string(value)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return iso8583.Message{}, "91"
	}
	s.config.Logger.Printf("event=http_upstream component=server direction=request command=%s url=%s", command, s.config.MessageURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.MessageURL, bytes.NewReader(body))
	if err != nil {
		return iso8583.Message{}, "91"
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.config.HTTPClient.Do(req)
	if err != nil {
		s.config.Logger.Printf("event=http_upstream component=server direction=response command=%s status=error error=%q", command, err)
		return iso8583.Message{}, "91"
	}
	defer resp.Body.Close()
	s.config.Logger.Printf("event=http_upstream component=server direction=response command=%s status=%d", command, resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return iso8583.Message{}, "91"
	}

	var decoded map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return iso8583.Message{}, "91"
	}
	fields := make(map[int][]byte, len(decoded))
	for key, value := range decoded {
		field, err := strconv.Atoi(key)
		if err != nil || field < 0 || field > 128 {
			return iso8583.Message{}, "91"
		}
		fields[field] = []byte(value)
	}
	if len(fields[0]) != 4 || len(fields[39]) != 2 {
		return iso8583.Message{}, "91"
	}
	return iso8583.Message{Fields: fields}, ""
}
func responseMTI(mti string) string {
	if len(mti) == 4 {
		return mti[:2] + "10"
	}
	return "0210"
}
func responseTo(request iso8583.Message, mti, code string) iso8583.Message {
	fields := make(map[int][]byte, len(request.Fields)+2)
	for n, v := range request.Fields {
		if n != 1 {
			fields[n] = append([]byte(nil), v...)
		}
	}
	fields[0] = []byte(mti)
	fields[39] = []byte(code)
	return iso8583.Message{Fields: fields}
}

// responseToNetwork builds the required response shape for 0800
// network-management requests. It echoes correlation/routing fields,
// adds DE39, and does not echo DE48 even if it was supplied.
func responseToNetwork(request iso8583.Message, code string) iso8583.Message {
	fields := make(map[int][]byte, 7)
	for _, number := range []int{7, 11, 15, 33, 70} {
		if value, ok := request.Fields[number]; ok {
			fields[number] = append([]byte(nil), value...)
		}
	}
	fields[0] = []byte("0810")
	fields[39] = []byte(code)
	return iso8583.Message{Fields: fields}
}

// HTTPHandler exposes the POST /call JSON API. Field keys are ISO numbers as strings.
func HTTPHandler(client *Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		fields := map[int][]byte{}
		for key, value := range input {
			n, err := strconv.Atoi(key)
			if err == nil && n >= 0 && n <= 128 {
				fields[n] = []byte(value)
			}
		}
		requestMessage := iso8583.Message{Fields: fields}
		logISO(client.logger, "http-api", "request", r.RemoteAddr, requestMessage)
		response, err := client.Call(r.Context(), requestMessage)
		if err != nil {
			client.logger.Printf("event=http_api component=client direction=response peer=%s status=bad_gateway error=%q", r.RemoteAddr, err)
			http.Error(w, "ISO call failed", http.StatusBadGateway)
			return
		}
		logISO(client.logger, "http-api", "response", r.RemoteAddr, response)
		output := map[string]string{}
		for n, value := range response.Fields {
			output[strconv.Itoa(n)] = string(value)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(output)
	})
}
