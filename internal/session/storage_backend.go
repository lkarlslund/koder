package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
)

func sessionCollection(st *store.Store) store.Collection[domain.Session] {
	return store.NewCollection(st, store.CollectionSpec[domain.Session]{
		Namespace: "sessions",
		GetID:     func(v domain.Session) string { return v.ID },
		SetID:     func(v *domain.Session, id string) { v.ID = id },
	})
}

func createSessionRecord(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, title, providerID, modelID, permissionProfile string, parentID *id.ID) (domain.Session, error) {
	return createSessionRecordWithOptions(ctx, st, chatsSrc, createSessionOptions{
		Title: title, TitleUserDefined: strings.TrimSpace(title) != "", ProviderID: providerID, ModelID: modelID, PermissionProfile: permissionProfile, ParentID: parentID,
		InitialChatRole: chatrole.Orchestrator,
	})
}

type createSessionOptions struct {
	ID                     id.ID
	Title                  string
	TitleUserDefined       bool
	ProviderID             string
	ModelID                string
	PermissionProfile      string
	ParentID               *id.ID
	Kind                   domain.SessionKind
	ProjectRoot            string
	ProjectRootManaged     bool
	InitialChatRole        domain.WorkflowRole
	InitialChatBackend     domain.ChatBackend
	InitialInteractionMode domain.InteractionMode
	InitialToolStates      domain.ToolStates
	InitialMilestoneKey    string
	InitialTaskRef         string
}

func createSessionRecordWithOptions(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, opts createSessionOptions) (domain.Session, error) {
	now := time.Now().UTC()
	sessionID := opts.ID
	if sessionID == "" {
		sessionID = id.NewAt(now)
	}
	role := opts.InitialChatRole
	if role == "" {
		role = chatrole.Orchestrator
	}
	session := domain.Session{
		ID:                 sessionID,
		ParentID:           opts.ParentID,
		Kind:               opts.Kind,
		Title:              opts.Title,
		TitleUserDefined:   opts.TitleUserDefined,
		PermissionProfile:  opts.PermissionProfile,
		PermissionRules:    nil,
		ToolStates:         map[domain.ToolKind]bool{},
		AccessSettings:     accesssettings.Default(),
		ProjectRoot:        opts.ProjectRoot,
		ProjectRootManaged: opts.ProjectRootManaged,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := sessionCollection(st).Put(ctx, session); err != nil {
		return domain.Session{}, err
	}
	if chatsSrc == nil {
		return domain.Session{}, fmt.Errorf("chat source is required")
	}
	if _, err := chatsSrc.CreateRecord(ctx, chatpkg.CreateRecordRequest{
		Session:           session,
		Title:             "Main",
		TitleUserDefined:  false,
		Role:              role,
		Backend:           opts.InitialChatBackend,
		InteractionMode:   opts.InitialInteractionMode,
		ProviderID:        opts.ProviderID,
		ModelID:           opts.ModelID,
		PermissionProfile: opts.PermissionProfile,
		ToolStates:        opts.InitialToolStates,
		MilestoneKey:      opts.InitialMilestoneKey,
		TaskRef:           opts.InitialTaskRef,
		Position:          0,
	}); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func listSessionRecords(ctx context.Context, st *store.Store) ([]domain.Session, error) {
	sessions, err := sessionCollection(st).List(ctx, store.All[domain.Session]())
	if err != nil {
		return nil, err
	}
	slices.SortFunc(sessions, func(a, b domain.Session) int {
		switch {
		case a.UpdatedAt.After(b.UpdatedAt):
			return -1
		case a.UpdatedAt.Before(b.UpdatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return sessions, nil
}

func getSessionRecord(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Session, error) {
	return sessionCollection(st).Get(ctx, sessionID)
}

func putSessionRecord(ctx context.Context, st *store.Store, session domain.Session) error {
	if session.ID == "" {
		return fmt.Errorf("put session: id is required")
	}
	return sessionCollection(st).Put(ctx, session)
}

func deleteSessionRecord(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, planSrc *planning.Source, sessionID id.ID) error {
	if sessionID == "" {
		return fmt.Errorf("delete session: session id is required")
	}
	if chatsSrc == nil {
		return fmt.Errorf("chat source is required")
	}
	if planSrc == nil {
		return fmt.Errorf("planning source is required")
	}
	if err := chatsSrc.DeleteSessionData(ctx, sessionID); err != nil {
		return err
	}
	if err := planSrc.DeleteSessionData(ctx, sessionID); err != nil {
		return err
	}
	return sessionCollection(st).Delete(ctx, sessionID)
}
