package voiceapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/phonedevice"
)

const deviceReadLimit = phonedevice.MaxArtifactBytes*4/3 + 512*1024
const deviceActionTimeout = 2 * time.Minute

type deviceFrame struct {
	Type                 string              `json:"type"`
	Protocol             string              `json:"protocol"`
	Capabilities         []string            `json:"capabilities,omitempty"`
	ConfirmationPolicies map[string]string   `json:"confirmation_policies,omitempty"`
	RequestID            string              `json:"request_id,omitempty"`
	Action               phonedevice.Action  `json:"action,omitempty"`
	Arguments            map[string]string   `json:"arguments,omitempty"`
	Result               *phonedevice.Result `json:"result,omitempty"`
	Error                string              `json:"error,omitempty"`
}

type deviceResponse struct {
	result phonedevice.Result
	err    error
}

type devicePeer struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan deviceResponse
	closed  error
}

func newDevicePeer(conn *websocket.Conn) *devicePeer {
	return &devicePeer{conn: conn, pending: map[string]chan deviceResponse{}}
}

func (h *Handler) servePhoneDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Devices == nil || h.Lease == nil {
		http.Error(w, "phone device tools unavailable", http.StatusServiceUnavailable)
		return
	}
	callID := strings.TrimSpace(r.URL.Query().Get("call_id"))
	if callID == "" || !h.Lease.OwnsDevice(voiceDeviceLeaseID(r), callID) {
		http.Error(w, "voice call is not active", http.StatusConflict)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(deviceReadLimit)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ctx := r.Context()
	var hello deviceFrame
	if err := readDeviceFrame(ctx, conn, &hello); err != nil || hello.Type != "device_hello" || hello.Protocol != protocolVersion {
		_ = conn.Close(websocket.StatusPolicyViolation, "device_hello required")
		return
	}
	peer := newDevicePeer(conn)
	release, err := h.Devices.AttachWithPolicies(callID, hello.Capabilities, hello.ConfirmationPolicies, peer.execute)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	defer release()
	defer peer.close(errors.New("phone device disconnected"))
	capabilities := h.Devices.CapabilitiesForCall(callID)
	accepted := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		accepted = append(accepted, string(capability.Action))
	}
	if err := peer.write(ctx, deviceFrame{Type: "device_ready", Protocol: protocolVersion, Capabilities: accepted}); err != nil {
		return
	}
	for {
		var frame deviceFrame
		if err := readDeviceFrame(ctx, conn, &frame); err != nil {
			return
		}
		if frame.Protocol != protocolVersion || frame.Type != "device_tool_result" || strings.TrimSpace(frame.RequestID) == "" {
			_ = peer.write(ctx, deviceFrame{Type: "device_error", Protocol: protocolVersion, Error: "device_tool_result required"})
			continue
		}
		var response deviceResponse
		switch {
		case strings.TrimSpace(frame.Error) != "":
			response.err = errors.New(strings.TrimSpace(frame.Error))
		case frame.Result == nil:
			response.err = errors.New("phone result is missing")
		default:
			response.result = *frame.Result
		}
		peer.complete(frame.RequestID, response)
	}
}

func (p *devicePeer) execute(ctx context.Context, callID string, action phonedevice.Action, args map[string]string) (phonedevice.Result, error) {
	if p == nil || p.conn == nil {
		return phonedevice.Result{}, errors.New("phone device connection is unavailable")
	}
	requestID := string(id.New())
	result := make(chan deviceResponse, 1)
	p.mu.Lock()
	if p.closed != nil {
		err := p.closed
		p.mu.Unlock()
		return phonedevice.Result{}, err
	}
	p.pending[requestID] = result
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, requestID)
		p.mu.Unlock()
	}()
	actionCtx, cancel := context.WithTimeout(ctx, deviceActionTimeout)
	defer cancel()
	if err := p.write(actionCtx, deviceFrame{
		Type: "device_tool_request", Protocol: protocolVersion, RequestID: requestID, Action: action, Arguments: args,
	}); err != nil {
		return phonedevice.Result{}, err
	}
	select {
	case <-actionCtx.Done():
		return phonedevice.Result{}, fmt.Errorf("wait for phone confirmation: %w", actionCtx.Err())
	case response := <-result:
		return response.result, response.err
	}
}

func (p *devicePeer) write(ctx context.Context, frame deviceFrame) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return p.conn.Write(ctx, websocket.MessageText, payload)
}

func (p *devicePeer) complete(requestID string, response deviceResponse) {
	p.mu.Lock()
	result := p.pending[strings.TrimSpace(requestID)]
	p.mu.Unlock()
	if result != nil {
		select {
		case result <- response:
		default:
		}
	}
}

func (p *devicePeer) close(err error) {
	p.mu.Lock()
	if p.closed != nil {
		p.mu.Unlock()
		return
	}
	p.closed = err
	pending := make([]chan deviceResponse, 0, len(p.pending))
	for _, result := range p.pending {
		pending = append(pending, result)
	}
	p.pending = map[string]chan deviceResponse{}
	p.mu.Unlock()
	for _, result := range pending {
		select {
		case result <- deviceResponse{err: err}:
		default:
		}
	}
}

func readDeviceFrame(ctx context.Context, conn *websocket.Conn, frame *deviceFrame) error {
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageText {
		return errors.New("phone device frames must be text")
	}
	if err := json.Unmarshal(payload, frame); err != nil {
		return fmt.Errorf("decode phone device frame: %w", err)
	}
	return nil
}
