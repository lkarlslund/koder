package session

import (
	"context"
	"fmt"
	"slices"
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

func createSessionRecord(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, title, providerID, modelID string, parentID *id.ID) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{
		ID:                id.NewAt(now),
		ParentID:          parentID,
		Title:             title,
		PermissionProfile: "",
		PermissionRules:   nil,
		ToolStates:        map[domain.ToolKind]bool{},
		AccessSettings:    accesssettings.Default(),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := sessionCollection(st).Put(ctx, session); err != nil {
		return domain.Session{}, err
	}
	if chatsSrc == nil {
		return domain.Session{}, fmt.Errorf("chat source is required")
	}
	if _, err := chatsSrc.CreateRecord(ctx, chatpkg.CreateRecordRequest{
		Session:    session,
		Title:      "Main",
		Role:       chatrole.Orchestrator,
		ProviderID: providerID,
		ModelID:    modelID,
		Position:   0,
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
