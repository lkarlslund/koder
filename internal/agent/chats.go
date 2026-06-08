package agent

import (
	"context"
	"fmt"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

func (e *Engine) Chat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	return chatpkg.Load(ctx, session, chatRecord, e.ChatDeps(), nil)
}

func (e *Engine) MetadataChat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	return chatpkg.LoadMetadata(ctx, session, chatRecord, e.ChatDeps(), nil)
}

func (e *Engine) ChatDeps() chatpkg.Deps {
	return chatpkg.Deps{
		Store:   e.store,
		Prompt:  e,
		Turns:   e,
		Tools:   e,
		Runtime: e,
		Life:    e,
		Pending: e,
		Compact: e,
		Errors:  e,
	}
}

func (e *Engine) ListChats(ctx context.Context, sessionID id.ID) ([]chattool.Status, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return owner.ChatToolControl("").ListChats(ctx, sessionID)
}

func (e *Engine) StartChat(ctx context.Context, sessionID, parentChatID id.ID, req chattool.StartRequest) (chattool.Status, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return chattool.Status{}, err
	}
	return owner.ChatToolControl(parentChatID).StartChat(ctx, sessionID, parentChatID, req)
}

func (e *Engine) UpdateChat(ctx context.Context, sessionID, ownerChatID, chatID id.ID, update chattool.UpdateRequest) (chattool.Status, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return chattool.Status{}, err
	}
	return owner.ChatToolControl(ownerChatID).UpdateChat(ctx, sessionID, ownerChatID, chatID, update)
}
