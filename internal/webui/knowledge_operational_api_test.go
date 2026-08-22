package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeOperationalStatusAndIndexRebuildAPI(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	response := knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.OperationalStatusPath, "")
	var unauthorized knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &unauthorized)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d response=%#v", response.StatusCode, unauthorized)
	}

	token := bindKnowledgeTestDevice(t, srv)
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.OperationalStatusPath, token)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var status knowledgeapi.OperationalStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if response.StatusCode != http.StatusOK || status.Status.Store.Backend != "memory" || status.Status.Store.IndexGeneration != 1 ||
		!status.Status.MaintenanceAvailable || status.Status.LexicalIndex == nil || status.Status.MutationCheckpoint.StreamID == "" {
		t.Fatalf("status=%d response=%#v", response.StatusCode, status)
	}
	if strings.Contains(string(body), `"path"`) {
		t.Fatalf("operational response leaked backend path: %s", body)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.IndexRebuildPath, token, knowledgeapi.IndexRebuildRequest{Index: "semantic"})
	var invalid knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid.Error == nil || invalid.Error.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("invalid rebuild status=%d response=%#v", response.StatusCode, invalid)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.IndexRebuildPath, token, knowledgeapi.IndexRebuildRequest{Index: "lexical"})
	var accepted knowledgeapi.IndexRebuildResponse
	decodeKnowledgeResponse(t, response, &accepted)
	if response.StatusCode != http.StatusAccepted || !accepted.Result.Accepted || !accepted.Result.Status.Running || accepted.Result.Status.TargetGeneration != 2 {
		t.Fatalf("rebuild status=%d response=%#v", response.StatusCode, accepted)
	}

	deadline := time.Now().Add(time.Second)
	for {
		response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.OperationalStatusPath, token)
		decodeKnowledgeResponse(t, response, &status)
		if response.StatusCode == http.StatusOK && status.Status.Store.IndexGeneration == 2 && status.Status.LexicalIndex != nil && !status.Status.LexicalIndex.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rebuild did not complete: status=%d response=%#v", response.StatusCode, status)
		}
	}
}
