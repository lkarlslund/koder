package provider

import (
	"maps"

	"github.com/lkarlslund/koder/internal/config"
)

// WithoutThinking turns off the thinking controls that req sends: the chat
// template's enable_thinking, DashScope's top-level enable_thinking, and
// reasoning_effort. It reports false when req sends none of them, since the
// provider's default then cannot be changed safely.
func WithoutThinking(req ChatRequest) (ChatRequest, bool) {
	body := maps.Clone(req.ExtraBody)
	changed := false
	if kwargs, ok := body["chat_template_kwargs"].(map[string]any); ok {
		kwargs = maps.Clone(kwargs)
		kwargs["enable_thinking"] = false
		if _, ok := kwargs["preserve_thinking"]; ok {
			kwargs["preserve_thinking"] = false
		}
		body["chat_template_kwargs"] = kwargs
		changed = true
	}
	for _, key := range []string{"enable_thinking", "preserve_thinking"} {
		if _, ok := body[key]; ok {
			body[key] = false
			changed = true
		}
	}
	for _, key := range []string{"reasoning_effort", "thinking_budget"} {
		if _, ok := body[key]; ok {
			delete(body, key)
			changed = true
		}
	}
	if !changed {
		return req, false
	}
	req.ExtraBody = body
	return req, true
}

// WithoutThinking turns thinking off for a request to this provider. Beyond
// the controls req already sends, a server that takes chat template
// arguments is sent enable_thinking=false, which llama.cpp, vLLM and SGLang
// servers pass to the model's template; thinking models such as Qwen think by
// default without being asked.
func (c *Client) WithoutThinking(req ChatRequest) (ChatRequest, bool) {
	if next, ok := WithoutThinking(req); ok {
		return next, true
	}
	if c == nil || !c.templateKwargs {
		return req, false
	}
	body := maps.Clone(req.ExtraBody)
	if body == nil {
		body = map[string]any{}
	}
	body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	req.ExtraBody = body
	return req, true
}

// takesTemplateKwargs reports whether probing found the server takes chat
// template arguments, or its transport is one that does.
func takesTemplateKwargs(cfg config.Provider) bool {
	if cfg.Features != nil && cfg.Features.TemplateKwargs {
		return true
	}
	switch OverlayTransport(cfg) {
	case config.TransportLlama, config.TransportNinfer:
		return true
	default:
		return false
	}
}
