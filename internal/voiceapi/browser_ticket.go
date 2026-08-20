package voiceapi

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"
	"time"
)

const (
	browserTicketProtocolPrefix = "koder-browser."
	browserTicketTTL            = 30 * time.Second
)

type browserTicket struct {
	deviceID  string
	expiresAt time.Time
}

// browserTickets grants one WebSocket upgrade from the already-connected web
// UI without exposing an Android device bearer token to browser JavaScript.
type browserTickets struct {
	mu      sync.Mutex
	tickets map[string]browserTicket
}

func (t *browserTickets) mint(deviceID string) (string, time.Time, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := time.Now().Add(browserTicketTTL)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tickets == nil {
		t.tickets = make(map[string]browserTicket)
	}
	now := time.Now()
	for key, ticket := range t.tickets {
		if !ticket.expiresAt.After(now) {
			delete(t.tickets, key)
		}
	}
	t.tickets[token] = browserTicket{deviceID: strings.TrimSpace(deviceID), expiresAt: expiresAt}
	return token, expiresAt, nil
}

func (t *browserTickets) consumeProtocol(header string) (string, bool) {
	for _, offered := range strings.Split(header, ",") {
		offered = strings.TrimSpace(offered)
		if !strings.HasPrefix(offered, browserTicketProtocolPrefix) {
			continue
		}
		token := strings.TrimPrefix(offered, browserTicketProtocolPrefix)
		t.mu.Lock()
		ticket, ok := t.tickets[token]
		delete(t.tickets, token)
		t.mu.Unlock()
		if ok && ticket.expiresAt.After(time.Now()) && ticket.deviceID != "" {
			return ticket.deviceID, true
		}
	}
	return "", false
}
