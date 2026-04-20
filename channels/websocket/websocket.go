// Package websocket is a minimal WebSocket egress channel. It implements the
// RFC 6455 server handshake and frame protocol directly so we don't need an
// external websocket dependency.
package websocket

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hkuds/nanobot-go/channels/base"
	"github.com/hkuds/nanobot-go/internal/bus"
	"github.com/hkuds/nanobot-go/internal/loop"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Options configures the websocket channel.
type Options struct {
	Host     string
	Port     int
	Path     string
	Loop     *loop.Loop
	Bus      *bus.Bus
	Token    string // shared bearer token (optional)
}

// Channel is a minimal WS server.
type Channel struct {
	opts     Options
	server   *http.Server

	mu      sync.Mutex
	clients map[string]*wsConn
}

type wsConn struct {
	id     string
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

// New constructs a Channel.
func New(opts Options) *Channel {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = 18790
	}
	if opts.Path == "" {
		opts.Path = "/ws"
	}
	return &Channel{opts: opts, clients: make(map[string]*wsConn)}
}

// Name returns "websocket".
func (c *Channel) Name() string { return "websocket" }

// SupportsStreaming returns true.
func (c *Channel) SupportsStreaming() bool { return true }

// Start begins listening.
func (c *Channel) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(c.opts.Path, c.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	addr := fmt.Sprintf("%s:%d", c.opts.Host, c.opts.Port)
	c.server = &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := c.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("websocket channel: %v", err)
		}
	}()
	log.Printf("websocket channel listening on ws://%s%s", addr, c.opts.Path)
	return nil
}

// Stop shuts down.
func (c *Channel) Stop(ctx context.Context) error {
	if c.server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.server.Shutdown(shutdownCtx)
}

// Send writes a full-text message event to the chat id's connection.
func (c *Channel) Send(ctx context.Context, m bus.OutboundMessage) error {
	return c.fanout(m.ChatID, map[string]any{"event": "message", "chat_id": m.ChatID, "text": m.Content})
}

// SendDelta emits stream deltas.
func (c *Channel) SendDelta(ctx context.Context, m bus.OutboundMessage) error {
	if v, _ := m.Metadata[bus.MetaStreamEnd].(bool); v {
		return c.fanout(m.ChatID, map[string]any{"event": "stream_end", "chat_id": m.ChatID})
	}
	return c.fanout(m.ChatID, map[string]any{"event": "delta", "chat_id": m.ChatID, "text": m.Content})
}

func (c *Channel) fanout(chatID string, payload map[string]any) error {
	data, _ := json.Marshal(payload)
	c.mu.Lock()
	conns := make([]*wsConn, 0, len(c.clients))
	for _, cl := range c.clients {
		conns = append(conns, cl)
	}
	c.mu.Unlock()
	for _, cl := range conns {
		if err := cl.WriteText(string(data)); err != nil {
			cl.Close()
		}
	}
	return nil
}

// --- handshake + framing ---------------------------------------------------

func (c *Channel) handleWS(w http.ResponseWriter, r *http.Request) {
	if c.opts.Token != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if token != c.opts.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "not a websocket upgrade", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	accept := acceptKey(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		conn.Close()
		return
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return
	}
	id := fmt.Sprintf("ws-%d", time.Now().UnixNano())
	cl := &wsConn{id: id, conn: conn}
	c.mu.Lock()
	c.clients[id] = cl
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.clients, id)
		c.mu.Unlock()
		_ = conn.Close()
	}()
	// Send "ready" event.
	ready, _ := json.Marshal(map[string]any{"event": "ready", "chat_id": id, "client_id": id})
	_ = cl.WriteText(string(ready))

	for {
		op, payload, err := readFrame(conn)
		if err != nil {
			return
		}
		switch op {
		case 0x8: // close
			return
		case 0x9: // ping
			_ = cl.writeFrame(0xA, payload)
		case 0x1: // text
			c.ingest(cl, string(payload))
		}
	}
}

func (c *Channel) ingest(cl *wsConn, text string) {
	if c.opts.Loop == nil || c.opts.Bus == nil {
		return
	}
	var env struct {
		Type    string `json:"type"`
		ChatID  string `json:"chat_id"`
		Content string `json:"content"`
		Text    string `json:"text"`
		Message string `json:"message"`
	}
	content := text
	if err := json.Unmarshal([]byte(text), &env); err == nil {
		if env.Content != "" {
			content = env.Content
		} else if env.Text != "" {
			content = env.Text
		} else if env.Message != "" {
			content = env.Message
		}
	}
	chatID := cl.id
	if env.ChatID != "" {
		chatID = env.ChatID
	}
	msg := bus.InboundMessage{
		Channel: "websocket",
		ChatID:  chatID,
		Content: content,
		Metadata: map[string]any{bus.MetaWantsStream: true},
	}
	go func() {
		_ = c.opts.Bus.PublishInbound(context.Background(), msg)
	}()
}

func acceptKey(k string) string {
	h := sha1.New()
	h.Write([]byte(k + wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func readFrame(r io.Reader) (op byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7F)
	switch {
	case length == 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case length == 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}

// WriteText writes a text frame.
func (c *wsConn) WriteText(s string) error { return c.writeFrame(0x1, []byte(s)) }

// Close closes the connection.
func (c *wsConn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.writeFrame(0x8, nil)
	_ = c.conn.Close()
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("closed")
	}
	hdr := []byte{0x80 | op, 0}
	length := len(payload)
	switch {
	case length < 126:
		hdr[1] = byte(length)
	case length < 65536:
		hdr[1] = 126
		hdr = append(hdr, 0, 0)
		binary.BigEndian.PutUint16(hdr[2:], uint16(length))
	default:
		hdr[1] = 127
		hdr = append(hdr, make([]byte, 8)...)
		binary.BigEndian.PutUint64(hdr[2:], uint64(length))
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

var _ base.Channel = (*Channel)(nil)
