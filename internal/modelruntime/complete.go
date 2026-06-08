package modelruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tokenestimate"
)

func (r *Runtime) BeginModelTurn(ctx context.Context, sessionID, chatID id.ID, step int, out chan<- domain.Event) error {
	if err := r.awaitOutstandingCaveman(ctx, chatID, out); err != nil {
		return err
	}
	r.RecordLifecycle(sessionID, "model_turn_started", "", map[string]string{"step": strconv.Itoa(step)})
	return nil
}

func (r *Runtime) CompleteModelRequest(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client, out chan<- domain.Event, req provider.ChatRequest, assistantItem domain.TimelineItem) (chatpkg.ModelResponse, error) {
	resp, streamed, cavemanJob, err := r.chatWithRetry(ctx, session, chat, client, out, req, assistantItem)
	if err != nil {
		return chatpkg.ModelResponse{}, err
	}
	reasoning, err := r.reasoningContentForResponse(ctx, chat, client, resp.Reasoning, cavemanJob, out)
	if err != nil {
		return chatpkg.ModelResponse{}, err
	}
	return chatpkg.ModelResponse{
		Text:           resp.Text,
		RawReasoning:   resp.Reasoning,
		Reasoning:      reasoning,
		Usage:          resp.Usage,
		ToolCalls:      resp.ToolCalls,
		ToolCallErrors: resp.ToolCallErrors,
		Streamed:       streamed,
	}, nil
}

func DefaultRetryPause(ctx context.Context, delay time.Duration, onTick func(time.Duration)) error {
	return waitForRetry(ctx, delay, onTick)
}

func (r *Runtime) chatWithRetry(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client, out chan<- domain.Event, req provider.ChatRequest, streamItem domain.TimelineItem) (provider.ChatResponse, bool, cavemanJob, error) {
	sessionID := session.ID
	providerID := chat.ProviderID
	promptProgressPending := r.promptProgressProbePending(providerID) && provider.RequestsPromptProgress(req)
	promptProgressRetried := false
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if err := r.awaitOutstandingCaveman(ctx, chat.ID, out); err != nil {
				return provider.ChatResponse{}, false, cavemanJob{}, err
			}
		}
		var (
			resp           provider.ChatResponse
			err            error
			streamed       bool
			receivedStream bool
			caveman        cavemanJob
			cavemanErr     error
		)
		if req.Stream {
			var reasoning strings.Builder
			reasoningSeen := false
			cavemanStarted := false
			startCaveman := func() {
				if cavemanStarted || !reasoningSeen {
					return
				}
				if !r.shouldCavemanThinking(reasoning.String()) {
					return
				}
				cavemanStarted = true
				caveman, cavemanErr = r.startCavemanThinking(ctx, chat, client, reasoning.String(), out)
			}
			resp, err = client.StreamChatResponse(ctx, req, func(evt domain.Event) {
				receivedStream = true
				switch evt.Kind {
				case domain.EventKindReasoning:
					if evt.Text != "" {
						reasoningSeen = true
						reasoning.WriteString(evt.Text)
					}
				case domain.EventKindMessageDelta, domain.EventKindToolCallDelta:
					startCaveman()
				}
				if (evt.Kind == domain.EventKindMessageDelta || evt.Kind == domain.EventKindReasoning) && evt.Item.ID == "" {
					evt.Item = streamItem
				}
				if out != nil {
					out <- evt
				}
			})
			if cavemanErr == nil {
				startCaveman()
			}
			streamed = true
		} else {
			resp, err = client.CompleteChat(ctx, req)
		}
		if cavemanErr != nil {
			return provider.ChatResponse{}, streamed, cavemanJob{}, cavemanErr
		}
		if err == nil {
			if promptProgressPending {
				r.setPromptProgressSupport(providerID, true)
			}
			return resp, streamed, caveman, nil
		}
		if promptProgressPending && !promptProgressRetried && provider.ShouldRetryWithoutPromptProgress(err) {
			promptProgressRetried = true
			r.setPromptProgressSupport(providerID, false)
			promptProgressPending = false
			req = provider.WithoutPromptProgress(req)
			if out != nil {
				out <- domain.Event{Kind: domain.EventKindStatus, Text: "Prompt progress unsupported; retrying without it..."}
			}
			continue
		}
		var apiErr *provider.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
			if attempt >= maxRateLimitRetries {
				return provider.ChatResponse{}, streamed, cavemanJob{}, err
			}
			delay := apiErr.RetryAfter
			if delay <= 0 {
				delay = defaultRateLimitRetryWait
			}
			retryNumber := attempt + 1
			initialStatus := formatRateLimitRetryStatus(delay, retryNumber)
			r.RecordLifecycle(sessionID, "rate_limit_retry", initialStatus, map[string]string{
				"retry":       strconv.Itoa(retryNumber),
				"retry_after": delay.String(),
			})
			lastRemaining := time.Duration(-1)
			if err := r.retryPause(ctx, delay, func(remaining time.Duration) {
				if remaining == lastRemaining {
					return
				}
				lastRemaining = remaining
				if out != nil {
					out <- domain.Event{Kind: domain.EventKindStatus, Text: formatRateLimitRetryStatus(remaining, retryNumber)}
				}
			}); err != nil {
				return provider.ChatResponse{}, streamed, cavemanJob{}, err
			}
			continue
		}
		if !shouldRetryTransientChatError(err, req.Stream, receivedStream) || attempt >= maxTransientChatRetries {
			return provider.ChatResponse{}, streamed, cavemanJob{}, err
		}
		delay := transientChatRetryDelay(attempt)
		retryNumber := attempt + 1
		initialStatus := formatTransientRetryStatus(delay, retryNumber)
		r.RecordLifecycle(sessionID, "transport_retry", initialStatus, map[string]string{
			"retry":       strconv.Itoa(retryNumber),
			"retry_after": delay.String(),
			"error":       err.Error(),
		})
		lastRemaining := time.Duration(-1)
		if err := r.retryPause(ctx, delay, func(remaining time.Duration) {
			if remaining == lastRemaining {
				return
			}
			lastRemaining = remaining
			if out != nil {
				out <- domain.Event{Kind: domain.EventKindStatus, Text: formatTransientRetryStatus(remaining, retryNumber)}
			}
		}); err != nil {
			return provider.ChatResponse{}, streamed, cavemanJob{}, err
		}
	}
}

func (r *Runtime) reasoningContentForResponse(ctx context.Context, chat domain.Chat, chatClient *provider.Client, reasoning string, job cavemanJob, events chan<- domain.Event) (domain.ReasoningContent, error) {
	result := domain.ReasoningContent{Text: reasoning, Tokens: tokenestimate.Text(reasoning)}
	thinking, err := r.settings.Thinking(chat, r.cfg.Thinking.CavemanPrompt, r.preserveThinkingEnabled(chat))
	if err != nil {
		return domain.ReasoningContent{}, err
	}
	if strings.TrimSpace(reasoning) == "" || !thinking.CavemanEnabled {
		return result, nil
	}
	if !job.Valid() {
		var err error
		job, err = r.startCavemanThinking(ctx, chat, chatClient, reasoning, events)
		if err != nil {
			return domain.ReasoningContent{}, err
		}
	}
	if !job.Valid() {
		return result, nil
	}
	caveman, err := job.Await(ctx)
	r.clearOutstandingCaveman(chat.ID, job)
	if err != nil {
		return domain.ReasoningContent{}, fmt.Errorf("convert reasoning to caveman: %w", err)
	}
	result.Caveman = strings.TrimSpace(caveman)
	result.CavemanTokens = tokenestimate.Text(result.Caveman)
	return result, nil
}

func (r *Runtime) startCavemanThinking(ctx context.Context, chat domain.Chat, chatClient *provider.Client, reasoning string, events chan<- domain.Event) (cavemanJob, error) {
	if !r.shouldCavemanThinking(reasoning) {
		return cavemanJob{}, nil
	}
	thinking, err := r.settings.Thinking(chat, r.cfg.Thinking.CavemanPrompt, r.preserveThinkingEnabled(chat))
	if err != nil {
		return cavemanJob{}, err
	}
	providerID := thinking.Model.ProviderID
	modelID := thinking.Model.ModelID
	client := chatClient
	chatModel, err := r.settings.Model(chat)
	if err != nil {
		return cavemanJob{}, err
	}
	chatProviderID, chatModelID := chatModel.Model.ProviderID, chatModel.Model.ModelID
	if providerID != chatProviderID || modelID != chatModelID || client == nil {
		client, err = provider.New(providerID, thinking.Provider, r.debug)
		if err != nil {
			return cavemanJob{}, err
		}
	}
	req := provider.ChatRequest{
		SessionID: chat.SessionID,
		ChatID:    chat.ID,
		Model:     modelID,
		Messages:  cavemanThinkingMessages(thinking.Prompt, reasoning),
		Stream:    true,
		ExtraBody: cavemanThinkingExtraBody(thinking.Provider, thinking.Model),
	}
	if r.caveman == nil {
		r.caveman = newCavemanService(thinking.Parallelism)
	}
	job := r.caveman.Submit(ctx, func(jobCtx context.Context) (string, error) {
		resp, err := r.completeCavemanThinking(jobCtx, providerID, client, req, events)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Text), nil
	})
	r.setOutstandingCaveman(chat.ID, job)
	return job, nil
}

func (r *Runtime) setOutstandingCaveman(chatID id.ID, job cavemanJob) {
	if r == nil || chatID == "" || !job.Valid() {
		return
	}
	r.cavemanMu.Lock()
	if r.cavemanJobs == nil {
		r.cavemanJobs = map[id.ID]cavemanJob{}
	}
	r.cavemanJobs[chatID] = job
	r.cavemanMu.Unlock()
}

func (r *Runtime) clearOutstandingCaveman(chatID id.ID, job cavemanJob) {
	if r == nil || chatID == "" || !job.Valid() {
		return
	}
	r.cavemanMu.Lock()
	if existing, ok := r.cavemanJobs[chatID]; ok && existing.state == job.state {
		delete(r.cavemanJobs, chatID)
	}
	r.cavemanMu.Unlock()
}

func (r *Runtime) awaitOutstandingCaveman(ctx context.Context, chatID id.ID, out chan<- domain.Event) error {
	if r == nil || chatID == "" {
		return nil
	}
	r.cavemanMu.Lock()
	job := r.cavemanJobs[chatID]
	r.cavemanMu.Unlock()
	if !job.Valid() {
		return nil
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Waiting for caveman thinking...",
			Meta: map[string]string{"caveman": "started"},
		}
	}
	_, err := job.Await(ctx)
	r.clearOutstandingCaveman(chatID, job)
	if err != nil {
		return fmt.Errorf("wait for outstanding caveman: %w", err)
	}
	return nil
}

func (r *Runtime) shouldCavemanThinking(reasoning string) bool {
	thinking := r.settings.Snapshot().Thinking
	if strings.TrimSpace(reasoning) == "" || !thinking.CavemanEnabled {
		return false
	}
	minTokens := thinking.CavemanMinTokens
	if minTokens <= 0 {
		minTokens = config.DefaultCavemanMinTokens
	}
	return tokenestimate.Text(reasoning) >= minTokens
}

func (r *Runtime) completeCavemanThinking(ctx context.Context, providerID id.ID, client *provider.Client, req provider.ChatRequest, out chan<- domain.Event) (provider.ChatResponse, error) {
	promptProgressPending := r.promptProgressProbePending(providerID) && provider.RequestsPromptProgress(req)
	stream := func(req provider.ChatRequest) (provider.ChatResponse, error) {
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		streamedBytes := 0
		limited := false
		onEvent := func(evt domain.Event) {
			switch evt.Kind {
			case domain.EventKindMessageDelta, domain.EventKindReasoning:
				streamedBytes += len(evt.Text)
				if streamedBytes > cavemanThinkingMaxBytes && !limited {
					limited = true
					if out != nil {
						out <- domain.Event{
							Kind: domain.EventKindStatus,
							Text: fmt.Sprintf("Caveman thinking exceeded %s; stopping rewrite", formatBytes(cavemanThinkingMaxBytes)),
							Meta: map[string]string{"caveman": "streaming"},
						}
					}
					cancel()
					return
				}
				if out != nil && streamedBytes > 0 {
					out <- domain.Event{
						Kind: domain.EventKindStatus,
						Text: fmt.Sprintf("Streaming caveman thinking (%s)", formatBytes(streamedBytes)),
						Meta: map[string]string{"caveman": "streaming"},
					}
				}
			case domain.EventKindStatus:
				if out == nil || evt.Meta[domain.EventMetaPromptProgress] != "true" {
					return
				}
				if evt.Meta == nil {
					evt.Meta = map[string]string{}
				}
				evt.Meta["caveman"] = "progress"
				out <- evt
			}
		}
		resp, err := client.StreamChatResponse(streamCtx, req, onEvent)
		if limited && errors.Is(err, context.Canceled) {
			resp.Reasoning = ""
			return resp, nil
		}
		return resp, err
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Converting thinking to caveman...",
			Meta: map[string]string{"caveman": "started"},
		}
	}
	resp, err := stream(req)
	if err == nil {
		if promptProgressPending {
			r.setPromptProgressSupport(providerID, true)
		}
		return resp, nil
	}
	if promptProgressPending && provider.ShouldRetryWithoutPromptProgress(err) {
		r.setPromptProgressSupport(providerID, false)
		return stream(provider.WithoutPromptProgress(req))
	}
	return resp, err
}

func cavemanThinkingExtraBody(cfg config.Provider, model config.ModelConfig) map[string]any {
	body := provider.RequestExtraBody(cfg, model)
	if body == nil {
		body = map[string]any{}
	}
	body["max_tokens"] = cavemanThinkingMaxTokens
	if strings.Contains(strings.ToLower(cfg.BaseURL), "dashscope") {
		body["enable_thinking"] = false
		body["preserve_thinking"] = false
		return body
	}
	kwargs, ok := body["chat_template_kwargs"].(map[string]any)
	if !ok {
		kwargs = map[string]any{}
		body["chat_template_kwargs"] = kwargs
	}
	kwargs["enable_thinking"] = false
	kwargs["preserve_thinking"] = false
	return body
}

func cavemanThinkingMessages(prompt, reasoning string) []provider.Message {
	system := cavemanSystemPrompt(prompt)
	user := strings.TrimSpace(reasoning)
	if user != "" {
		user = "MODEL_THINKING:\n" + user
	}
	return []provider.Message{
		{Role: provider.RoleSystem, Content: system},
		{Role: provider.RoleUser, Content: user},
	}
}

func cavemanSystemPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return config.DefaultCavemanThinkingPrompt
	}
	if strings.Contains(prompt, "{{thinking}}") {
		prompt = strings.ReplaceAll(prompt, "{{thinking}}", "The MODEL_THINKING payload is provided in the user message.")
	}
	return strings.TrimSpace(prompt)
}

func (r *Runtime) promptProgressProbePending(providerID id.ID) bool {
	cfg, ok := r.providerConfig(providerID)
	return ok && provider.PromptProgressProbePending(cfg)
}

func (r *Runtime) providerConfig(providerID id.ID) (config.Provider, bool) {
	return r.cfg.Provider(string(providerID))
}

func (r *Runtime) setPromptProgressSupport(providerID id.ID, supported bool) {
	id := strings.TrimSpace(string(providerID))
	if id == "" || r.cfg.Providers == nil {
		return
	}
	cfg := r.cfg
	providerCfg, ok := cfg.Providers[id]
	if !ok {
		return
	}
	if providerCfg.PromptProgressProbed && providerCfg.PromptProgressSupported == supported {
		return
	}
	providerCfg.PromptProgressMode = config.NormalizePromptProgressMode(providerCfg.PromptProgressMode)
	providerCfg.PromptProgressProbed = true
	providerCfg.PromptProgressSupported = supported
	providers := make(map[string]config.Provider, len(cfg.Providers))
	for key, value := range cfg.Providers {
		providers[key] = value
	}
	providers[id] = providerCfg
	cfg.Providers = providers
	r.cfg = cfg
	if strings.TrimSpace(cfg.Path()) == "" {
		return
	}
	if err := cfg.Save(); err != nil {
		r.RecordLifecycle("", "prompt_progress_probe_save_failed", err.Error(), map[string]string{
			"provider":  id,
			"supported": strconv.FormatBool(supported),
		})
	}
}

func waitForRetry(ctx context.Context, delay time.Duration, onTick func(time.Duration)) error {
	if delay <= 0 {
		delay = defaultRateLimitRetryWait
	}
	remaining := roundRetryDelay(delay)
	if onTick != nil {
		onTick(remaining)
	}
	if remaining <= 0 {
		return nil
	}
	deadline := time.Now().Add(delay)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			next := time.Until(deadline)
			if next <= 0 {
				if onTick != nil {
					onTick(0)
				}
				return nil
			}
			rounded := roundRetryDelay(next)
			if onTick != nil {
				onTick(rounded)
			}
		}
	}
}

func formatRateLimitRetryStatus(delay time.Duration, retryNumber int) string {
	delay = roundRetryDelay(delay)
	return fmt.Sprintf("Working (rate limit hit, retrying in %s, retry %d)", delay, retryNumber)
}

func formatTransientRetryStatus(delay time.Duration, retryNumber int) string {
	delay = roundRetryDelay(delay)
	return fmt.Sprintf("Working (connection dropped, retrying in %s, retry %d)", delay, retryNumber)
}

func transientChatRetryDelay(attempt int) time.Duration {
	delay := defaultTransientRetryWait
	for i := 0; i < attempt; i++ {
		delay *= 3
	}
	return delay
}

func shouldRetryTransientChatError(err error, stream bool, receivedStream bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if stream && receivedStream {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 502 || apiErr.StatusCode == 503 || apiErr.StatusCode == 504
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func roundRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	delay = delay.Round(time.Second)
	if delay <= 0 {
		return time.Second
	}
	return delay
}

func formatBytes(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size) / 1024
	if value < 10 {
		return fmt.Sprintf("%.1f KB", value)
	}
	return fmt.Sprintf("%.0f KB", value)
}
