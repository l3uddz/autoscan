package api

import (
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins since authentication is handled by middleware
		return true
	},
}

// logsWebSocket handles GET /api/logs (WebSocket upgrade)
func (h *Handler) logsWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket upgrade failed")
		return
	}

	h.logHub.Register(conn)
	log.Debug().Msg("WebSocket client connected for logs")

	// Keep connection alive and handle disconnection
	go func() {
		defer func() {
			h.logHub.Unregister(conn)
			log.Debug().Msg("WebSocket client disconnected")
		}()

		for {
			// Read messages to detect disconnection
			// We don't expect any messages from the client
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}
