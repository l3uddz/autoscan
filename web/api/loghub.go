package api

import (
	"sync"

	"github.com/gorilla/websocket"
)

// LogHub manages WebSocket clients for log streaming
type LogHub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.RWMutex
}

// NewLogHub creates a new log hub and starts its run loop
func NewLogHub() *LogHub {
	hub := &LogHub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
	go hub.run()
	return hub
}

// run processes register, unregister, and broadcast events
func (h *LogHub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					// Mark for removal but don't modify map during iteration
					go func(c *websocket.Conn) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Write implements io.Writer for use with zerolog
// This allows the LogHub to receive log messages directly
func (h *LogHub) Write(p []byte) (n int, err error) {
	// Make a copy to avoid data races
	msg := make([]byte, len(p))
	copy(msg, p)

	// Non-blocking send to avoid slowing down logging
	select {
	case h.broadcast <- msg:
	default:
		// Drop message if buffer is full
	}
	return len(p), nil
}

// Register adds a WebSocket client to the hub
func (h *LogHub) Register(conn *websocket.Conn) {
	h.register <- conn
}

// Unregister removes a WebSocket client from the hub
func (h *LogHub) Unregister(conn *websocket.Conn) {
	h.unregister <- conn
}

// ClientCount returns the number of connected clients
func (h *LogHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
