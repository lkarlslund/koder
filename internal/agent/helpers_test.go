package agent

import (
	"context"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
)

func (e *Engine) buildConversation(ctx context.Context, sessionID, chatID id.ID) ([]provider.Message, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	session := owner.Snapshot().Session
	return e.buildConversationPreview(ctx, session, chatID, "", nil, nil, nil)
}
