package provider

import "maps"

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
