package modelruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/attachment"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
)

func (r *Runtime) PreparePromptTurn(ctx context.Context, rt *chatpkg.Chat, input domain.QueuedInput, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	if rt == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	chat := snapshot.Chat
	if err := r.validatePromptAttachments(chat, drafts); err != nil {
		return nil, err
	}
	user, err := r.userMessageForPrompt(session, prompt, drafts, refs)
	if err != nil {
		return nil, err
	}
	userItem, err := rt.AppendUserMessageForInput(ctx, input, user)
	if err != nil {
		return nil, err
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: userItem}
	r.RecordLifecycle(session.ID, "user_message_persisted", prompt, map[string]string{"item_id": userItem.ID})
	chat = rt.Snapshot().Chat
	client, err := r.ClientForChat(chat)
	if err != nil {
		return nil, err
	}
	if session.ID != "" && needsSessionAgentsRefresh(session) {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."}
	}
	session, err = r.ensureSessionAgents(ctx, session, chat, client)
	if err != nil {
		return nil, err
	}
	rt.SetSession(session)
	chat = rt.Snapshot().Chat
	r.RecordLifecycle(session.ID, "prompt_started", prompt, map[string]string{"provider": chat.ProviderID, "model": chat.ModelID})
	return chatpkg.TurnInstructionBlocks(note, ""), nil
}

func (r *Runtime) PrepareContinueTurn(ctx context.Context, rt *chatpkg.Chat, note string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	if rt == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	chat := snapshot.Chat
	client, err := r.ClientForChat(chat)
	if err != nil {
		return nil, err
	}
	if session.ID != "" && needsSessionAgentsRefresh(session) {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."}
	}
	session, err = r.ensureSessionAgents(ctx, session, chat, client)
	if err != nil {
		return nil, err
	}
	rt.SetSession(session)
	if strings.TrimSpace(note) != "" {
		r.RecordLifecycle(session.ID, "continue_with_note", note, nil)
	} else {
		r.RecordLifecycle(session.ID, "continue", "", nil)
	}
	return chatpkg.TurnInstructionBlocks(note, "Continue from where you left off."), nil
}

func (r *Runtime) MaxToolLoopSteps() int {
	if r.cfg.MaxToolLoopSteps > 0 {
		return r.cfg.MaxToolLoopSteps
	}
	return config.Default().MaxToolLoopSteps
}

func (r *Runtime) validatePromptAttachments(chat domain.Chat, drafts []attachment.Draft) error {
	for _, draft := range drafts {
		kind := attachment.ClassifyMIME(draft.MIME)
		switch kind {
		case attachment.KindText:
			continue
		case attachment.KindImage, attachment.KindPDF:
			supported, err := r.caps.SupportsAttachment(chat.ProviderID, providerCfgForChat(r.cfg, chat), chat.ModelID, kind)
			if err != nil {
				return err
			}
			if supported {
				continue
			}
			return fmt.Errorf("provider %s model %s does not support %s attachments", chat.ProviderID, chat.ModelID, kind)
		default:
			return fmt.Errorf("unsupported attachment type %q", draft.MIME)
		}
	}
	return nil
}

func (r *Runtime) userMessageForPrompt(session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft) (domain.UserMessage, error) {
	user := domain.UserMessage{Text: prompt}
	for _, draft := range drafts {
		meta, err := r.files.AdoptDraft(draft, session.ID)
		if err != nil {
			return domain.UserMessage{}, err
		}
		user.Attachments = append(user.Attachments, domain.Attachment{
			ID: meta.ID, Name: meta.Name, MIME: meta.MIME, Path: meta.Path, Size: meta.Size, Source: meta.Source, Original: meta.Original,
		})
	}
	for _, ref := range refs {
		user.References = append(user.References, domain.Reference{
			Kind:    string(ref.Kind),
			Path:    ref.Path,
			Display: ref.Display,
			Start:   ref.Start,
			End:     ref.End,
		})
	}
	return user, nil
}

func (r *Runtime) ensureSessionAgents(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (domain.Session, error) {
	if !needsSessionAgentsRefresh(session) {
		return session, nil
	}
	if r.sessions == nil {
		return domain.Session{}, fmt.Errorf("session source is required")
	}
	snapshot, err := r.agents.Discover(ctx, sessionProjectRoot(session))
	if err != nil {
		return domain.Session{}, err
	}
	_, modelID, err := chatModel(chat)
	if err != nil {
		return domain.Session{}, err
	}
	resolution, err := r.agents.Resolve(ctx, client, modelID, snapshot)
	if err != nil {
		return domain.Session{}, err
	}
	files := make([]domain.AgentsFile, 0, len(resolution.Snapshot.Files))
	for _, item := range resolution.Snapshot.Files {
		files = append(files, domain.AgentsFile{
			Path:         item.Path,
			Kind:         item.Kind,
			Priority:     item.Priority,
			ModTime:      item.ModTime,
			Checksum:     item.Checksum,
			Size:         item.Size,
			DiscoveredBy: item.DiscoveredBy,
		})
	}
	owner, err := r.sessions.LoadSession(ctx, session.ID)
	if err != nil {
		return domain.Session{}, err
	}
	return owner.UpdateSession(ctx, func(session *domain.Session) {
		session.ProjectChecksum = resolution.Snapshot.Checksum
		session.AgentsResolved = resolution.ResolvedAgents
		session.AgentsSummary = resolution.ConflictSummary
		session.AgentsFiles = append([]domain.AgentsFile(nil), files...)
		session.AgentsGeneratedAt = resolution.GeneratedAt
	})
}

func needsSessionAgentsRefresh(session domain.Session) bool {
	if strings.TrimSpace(session.ProjectChecksum) == "" {
		return true
	}
	return strings.TrimSpace(session.AgentsResolved) == "" && strings.TrimSpace(session.AgentsSummary) == ""
}
