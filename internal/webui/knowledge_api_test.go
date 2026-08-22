package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeChunkReadAndListRequireDeviceAuthentication(t *testing.T) {
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
	global := createAPIChunk(t, service, "Global reference", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	createAPIChunk(t, service, "Koder project", knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "koder"})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)

	response := knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks", "")
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") == "" {
		response.Body.Close()
		t.Fatalf("unauthenticated status=%d authenticate=%q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	var unauthorized knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &unauthorized)
	if unauthorized.APIVersion != knowledgeapi.Version || unauthorized.Error == nil || unauthorized.Error.Code != knowledgeService.ErrorCodeForbidden {
		t.Fatalf("unauthorized response = %#v", unauthorized)
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks?scope=global&limit=1", token)
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		t.Fatalf("list status = %d", response.StatusCode)
	}
	var listed knowledgeapi.ChunkListResponse
	decodeKnowledgeResponse(t, response, &listed)
	if listed.APIVersion != knowledgeapi.Version || listed.Page.Limit != 1 || listed.Page.Returned != 1 ||
		len(listed.Chunks) != 1 || listed.Chunks[0].ID != global.ID {
		t.Fatalf("list response = %#v", listed)
	}

	chunkURL := srv.URL() + knowledgeapi.RoutePrefix + "/chunks/" + string(global.ID)
	response = knowledgeAPIRequest(t, http.MethodGet, chunkURL, token)
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		t.Fatalf("get status = %d", response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	var got knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &got)
	if got.Chunk.ID != global.ID || got.ETag == "" || got.ETag != etag || got.ExplorerURL == "" {
		t.Fatalf("get response = %#v header etag=%q", got, etag)
	}

	request, err := http.NewRequest(http.MethodGet, chunkURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-None-Match", etag)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("conditional get: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional get status = %d", response.StatusCode)
	}
}

func TestKnowledgeChunkAPIHidesPolicyDeniedRecords(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: knowledgeService.ChunkPolicyFunc(func(_ context.Context, actor knowledge.Actor, action knowledgeService.ChunkPolicyAction, chunk knowledge.Chunk) error {
			if actor.Kind != knowledge.ActorKindUser || !strings.HasPrefix(actor.ID, "device:") || action != knowledgeService.ChunkPolicyRead {
				return nil
			}
			if chunk.Title == "Hidden" {
				return errors.New("private policy detail")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	hidden := createAPIChunk(t, service, "Hidden", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	visible := createAPIChunk(t, service, "Visible", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)
	response := knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks?sort=title", token)
	var listed knowledgeapi.ChunkListResponse
	decodeKnowledgeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Chunks) != 1 || listed.Chunks[0].ID != visible.ID {
		t.Fatalf("policy-filtered list status=%d response=%#v", response.StatusCode, listed)
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks/"+string(hidden.ID), token)
	var denied knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &denied)
	if response.StatusCode != http.StatusNotFound || denied.Error == nil || denied.Error.Code != knowledgeService.ErrorCodeNotFound {
		t.Fatalf("denied get status=%d response=%#v", response.StatusCode, denied)
	}
}

func createAPIChunk(t *testing.T, service *knowledgeService.Service, title string, scope knowledge.Scope) knowledge.Chunk {
	t.Helper()
	created, err := service.CreateChunk(context.Background(), knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: title, Kind: knowledge.ChunkKindReference, Scope: scope,
	}})
	if err != nil {
		t.Fatalf("create chunk %q: %v", title, err)
	}
	return created.Chunk
}

func bindKnowledgeTestDevice(t *testing.T, server *Server) string {
	t.Helper()
	invitation, err := server.devices.CreateInvitation()
	if err != nil {
		t.Fatalf("create device invitation: %v", err)
	}
	binding, err := server.devices.Bind(invitation.Code, deviceauth.DeviceInfo{Name: "Knowledge API test"})
	if err != nil {
		t.Fatalf("bind device: %v", err)
	}
	return binding.Token
}

func knowledgeAPIRequest(t *testing.T, method, url, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Knowledge API request: %v", err)
	}
	return response
}

func decodeKnowledgeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode Knowledge response: %v", err)
	}
}
