package voiceapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lkarlslund/koder/internal/phonesync"
)

// phoneSyncRequestLimit fits a full batch of maximum-length log lines.
const phoneSyncRequestLimit = 4 << 20

// PhoneSync stores what phones upload outside voice calls.
type PhoneSync interface {
	PhoneSyncStore() *phonesync.Store
	PhoneLogsEnabled() bool
}

// servePhoneSync takes a bound phone's log upload and alert reports, and
// answers with the alerts it should schedule. It needs no active call.
func (h *Handler) servePhoneSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Sync == nil || h.Sync.PhoneSyncStore() == nil {
		http.Error(w, "phone sync unavailable", http.StatusServiceUnavailable)
		return
	}
	deviceID, name := h.syncDevice(r)
	if deviceID == "" {
		http.Error(w, "phone sync needs a bound device", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, phoneSyncRequestLimit)
	var request phonesync.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid phone sync request", http.StatusBadRequest)
		return
	}
	if request.Protocol != protocolVersion {
		http.Error(w, "unsupported phone sync protocol", http.StatusBadRequest)
		return
	}
	response, err := h.Sync.PhoneSyncStore().Sync(deviceID, name, request, h.Sync.PhoneLogsEnabled())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Protocol = protocolVersion
	writeJSONResponse(w, http.StatusOK, response)
}

// syncDevice names the phone behind an authorized request: its registry
// binding, or with a shared legacy token, its installation id.
func (h *Handler) syncDevice(r *http.Request) (string, string) {
	info := deviceInfoFromHeaders(r.Header)
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = strings.TrimSpace(info.Manufacturer + " " + info.Model)
	}
	if h.Auth != nil {
		token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer "))
		device, ok := h.Auth.Authenticate(token, info)
		if !ok {
			return "", ""
		}
		if device.Name != "" {
			name = device.Name
		}
		return device.ID, name
	}
	if installation := strings.TrimSpace(info.InstallationID); installation != "" {
		return "installation-" + installation, name
	}
	return "", ""
}
