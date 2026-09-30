package agent

import (
	"context"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/modelruntime"
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

func (e *Engine) buildCompactionConversationForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) ([]provider.Message, string, error) {
	base := compactionBaseForNextCut(timeline, len(timeline))
	keepStart := base.MinKeepStart + modelruntime.PreservedTimelineToolCallTailStart(timeline[base.MinKeepStart:], e.Runtime.CompactionKeepToolCalls())
	return e.buildCompactionConversationForTimelinePrefix(session, chat, timeline, keepStart, base)
}
