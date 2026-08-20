package phonetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/tools"
)

const serviceKey = "phone_device"

// RuntimeService exposes an active phone provider to voice chats.
func RuntimeService(control phonedevice.Control) map[string]any {
	if control == nil {
		return nil
	}
	return map[string]any{serviceKey: control}
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Use connected phone",
		Description: "Use capabilities explicitly enabled on the connected Android phone.",
		ExposeToLLM: true,
	})
}

type tool struct{}

func (tool) ID() tools.ID             { return tools.Phone }
func (tool) BypassesPermission() bool { return true }
func (tool) Preview(req tools.Request) string {
	return "Phone: " + strings.ReplaceAll(req.Args["action"], "_", " ")
}

func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if runtime.ChatRole != chatrole.Voice {
		return tools.ToolSpec{}, false
	}
	control, err := service(runtime)
	if err != nil {
		return tools.ToolSpec{}, false
	}
	capabilities := control.Capabilities()
	if len(capabilities) == 0 {
		return tools.ToolSpec{}, false
	}
	actions := make([]string, 0, len(capabilities))
	lines := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		action := string(capability.Action)
		actions = append(actions, action)
		confirmation := ""
		if capability.Confirmation {
			confirmation = " The phone asks the user to confirm."
		}
		visibility := " Reads phone data without opening another app."
		if capability.UserFacing {
			visibility = " USER-FACING ACTION: visibly or audibly affects the phone; call only when the user explicitly requested this exact action, never to gather knowledge."
		}
		lines = append(lines, fmt.Sprintf("%s: %s. Arguments: %s.%s%s", action, capability.Summary, capability.Arguments, visibility, confirmation))
	}
	slices.Sort(actions)
	schema, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":           map[string]any{"type": "string", "enum": actions},
			"query":            map[string]any{"type": "string"},
			"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"phone_number":     map[string]any{"type": "string"},
			"contact_name":     map[string]any{"type": "string"},
			"contact_id":       map[string]any{"type": "string"},
			"event_id":         map[string]any{"type": "string"},
			"operation":        map[string]any{"type": "string", "enum": []string{"edit", "cancel"}},
			"message":          map[string]any{"type": "string"},
			"to":               map[string]any{"type": "string"},
			"subject":          map[string]any{"type": "string"},
			"body":             map[string]any{"type": "string"},
			"name":             map[string]any{"type": "string"},
			"email":            map[string]any{"type": "string"},
			"title":            map[string]any{"type": "string"},
			"start_time":       map[string]any{"type": "string", "description": "RFC 3339 timestamp"},
			"end_time":         map[string]any{"type": "string", "description": "RFC 3339 timestamp"},
			"since_time":       map[string]any{"type": "string", "description": "RFC 3339 timestamp"},
			"location":         map[string]any{"type": "string"},
			"description":      map[string]any{"type": "string"},
			"address":          map[string]any{"type": "string"},
			"note":             map[string]any{"type": "string"},
			"latitude":         map[string]any{"type": "number"},
			"longitude":        map[string]any{"type": "number"},
			"hour":             map[string]any{"type": "integer", "minimum": 0, "maximum": 23},
			"minute":           map[string]any{"type": "integer", "minimum": 0, "maximum": 59},
			"duration_seconds": map[string]any{"type": "integer", "minimum": 1},
			"label":            map[string]any{"type": "string"},
			"text":             map[string]any{"type": "string"},
			"url":              map[string]any{"type": "string"},
			"app":              map[string]any{"type": "string"},
			"package_name":     map[string]any{"type": "string"},
			"media_action":     map[string]any{"type": "string", "enum": []string{"play", "pause", "toggle", "next", "previous"}},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	})
	if err != nil {
		return tools.ToolSpec{}, false
	}
	spec.Usage = "Use only for an action on the currently connected phone. Actions marked USER-FACING ACTION open UI, cause sound, communicate, or change phone state. Call one only when the current user utterance explicitly requests that exact action; never call one to gain knowledge, perform research, or inspect its result. For current local information, first read get_location; if outside research is needed, use the standard research tools or coordinate a sibling chat with the resolved place name. Enabled actions:\n- " + strings.Join(lines, "\n- ")
	spec.Parameters = string(schema)
	return spec, true
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	action := strings.TrimSpace(args["action"])
	if action == "" {
		return nil, errors.New("action is required")
	}
	out := make(map[string]string, len(args))
	for key, value := range args {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	out["action"] = action
	return out, nil
}

func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	control, err := service(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	action := phonedevice.Action(opts.Request.Args["action"])
	args := make(map[string]string, len(opts.Request.Args)-1)
	for key, value := range opts.Request.Args {
		if key != "action" {
			args[key] = value
		}
	}
	result, err := control.Execute(ctx, action, args)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{Output: result.Text, Stored: result.Data}, nil
}

func service(runtime tools.Runtime) (phonedevice.Control, error) {
	control, err := tools.RequireService[phonedevice.Control](runtime, serviceKey)
	if err != nil {
		return nil, errors.New("connected phone is unavailable")
	}
	if resolver, ok := control.(interface {
		ForChat(string) phonedevice.Control
	}); ok {
		control = resolver.ForChat(string(runtime.ChatID))
	}
	return control, nil
}
