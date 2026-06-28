package modifier

import (
	"fmt"
	"time"
)

// WebSocketEntry holds data about a websocket message captured from CDP
type WebSocketEntry struct {
	ID        string    // unique entry ID
	URL       string    // WebSocket URL
	Direction string    // "sent" (outgoing from browser) or "received" (incoming to browser)
	Timestamp time.Time // timestamp
	Opcode    int       // WebSocket opcode
	Payload   string    // payload data
}

func OpcodeToString(opcode int) string {
	switch opcode {
	case 1:
		return "Text"
	case 2:
		return "Binary"
	case 8:
		return "Close"
	case 9:
		return "Ping"
	case 10:
		return "Pong"
	default:
		return fmt.Sprintf("0x%x", opcode)
	}
}

// InjectWebSocketMessage logs a websocket message from CDP capture.
func (l *Logger) InjectWebSocketMessage(msg *WebSocketEntry) {
	l.mu.Lock()
	if msg.ID != "" {
		if _, exists := l.wsEntries[msg.ID]; !exists {
			l.wsEntries[msg.ID] = msg
		}
	}
	l.mu.Unlock()

	if l.wsnotificationchan != nil {
		l.wsnotificationchan <- Notification{msg.ID, 2}
	}
}

// GetWebSocketEntry returns a specific WebSocketEntry by ID.
func (l *Logger) GetWebSocketEntry(id string) *WebSocketEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wsEntries[id]
}

// GetWebSocketEntries returns the wsEntries map.
func (l *Logger) GetWebSocketEntries() map[string]*WebSocketEntry {
	return l.wsEntries
}

// ResetWSEntries clears only the websocket entries.
func (l *Logger) ResetWSEntries() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.wsEntries = make(map[string]*WebSocketEntry)
}

// AddWebSocketEntry manually adds a websocket entry.
func (l *Logger) AddWebSocketEntry(e *WebSocketEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e.ID != "" {
		l.wsEntries[e.ID] = e
	}
}
