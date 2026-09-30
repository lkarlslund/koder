package session

import (
	"context"
	"strings"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

func createSessionRecord(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, title, providerID, modelID, permissionProfile string, parentID *id.ID) (domain.Session, error) {
	return createSessionRecordWithOptions(ctx, st, chatsSrc, createSessionOptions{
		Title: title, TitleUserDefined: strings.TrimSpace(title) != "", ProviderID: providerID, ModelID: modelID, PermissionProfile: permissionProfile, ParentID: parentID,
		InitialChatRole: chatrole.Orchestrator,
	})
}
