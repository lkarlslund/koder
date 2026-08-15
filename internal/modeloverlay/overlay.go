package modeloverlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/assets"
)

const (
	SelectionAuto    = "auto"
	SelectionDefault = "default"
	directoryName    = "model-overlays"
)

// Catalog contains all valid model overlays and non-fatal loading problems.
type Catalog struct {
	Templates []Template `json:"templates"`
	Problems  []Problem  `json:"problems,omitempty"`
}

// Problem describes one overlay file that could not be loaded.
type Problem struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

// Template declaratively describes model matching, UI controls, and request bindings.
type Template struct {
	Version     int       `json:"version"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Priority    int       `json:"priority,omitempty"`
	Match       Match     `json:"match"`
	Controls    []Control `json:"controls,omitempty"`
	Source      string    `json:"source,omitempty"`
}

// Match selects models and provider transports for an overlay.
type Match struct {
	ModelIDs   []string `json:"model_ids,omitempty"`
	Transports []string `json:"transports,omitempty"`
}

// Control defines one dynamically rendered model setting.
type Control struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Help        string    `json:"help,omitempty"`
	Type        string    `json:"type"`
	Default     any       `json:"default,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Min         *float64  `json:"min,omitempty"`
	Max         *float64  `json:"max,omitempty"`
	Step        *float64  `json:"step,omitempty"`
	Choices     []Choice  `json:"choices,omitempty"`
	Bindings    []Binding `json:"bindings,omitempty"`
}

// Choice is one select control option.
type Choice struct {
	Value any    `json:"value"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
}

// Binding maps a control value to one request JSON path.
type Binding struct {
	Path       string         `json:"path"`
	Transports []string       `json:"transports,omitempty"`
	OmitValues []any          `json:"omit_values,omitempty"`
	ValueMap   map[string]any `json:"value_map,omitempty"`
}

// Resolved is the deterministic merge of overlays for one model.
type Resolved struct {
	IDs      []string  `json:"ids"`
	Title    string    `json:"title"`
	Controls []Control `json:"controls"`
}

// Load reads embedded overlays and lets files in root/model-overlays override
// them by filename. Invalid user files are reported without disabling valid overlays.
func Load(root string) Catalog {
	files := map[string][]byte{}
	if defaults, err := assets.UserDefaults(); err == nil {
		for _, item := range defaults {
			target := filepath.ToSlash(item.Target)
			if strings.HasPrefix(target, directoryName+"/") && strings.HasSuffix(strings.ToLower(target), ".json") {
				files[filepath.Base(target)] = slices.Clone(item.Content)
			}
		}
	}
	dir := filepath.Join(strings.TrimSpace(root), directoryName)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				continue
			}
			if data, readErr := os.ReadFile(filepath.Join(dir, entry.Name())); readErr == nil {
				files[entry.Name()] = data
			} else {
				files[entry.Name()] = nil
			}
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	catalog := Catalog{}
	seen := map[string]string{}
	for _, name := range names {
		data := files[name]
		if data == nil {
			catalog.Problems = append(catalog.Problems, Problem{File: name, Error: "read failed"})
			continue
		}
		var overlay Template
		if err := json.Unmarshal(data, &overlay); err != nil {
			catalog.Problems = append(catalog.Problems, Problem{File: name, Error: err.Error()})
			continue
		}
		overlay.Source = name
		if err := validateTemplate(overlay); err != nil {
			catalog.Problems = append(catalog.Problems, Problem{File: name, Error: err.Error()})
			continue
		}
		if previous := seen[overlay.ID]; previous != "" {
			catalog.Problems = append(catalog.Problems, Problem{File: name, Error: fmt.Sprintf("overlay id %q already defined by %s", overlay.ID, previous)})
			continue
		}
		seen[overlay.ID] = name
		catalog.Templates = append(catalog.Templates, overlay)
	}
	slices.SortFunc(catalog.Templates, func(a, b Template) int {
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		return strings.Compare(a.ID, b.ID)
	})
	return catalog
}

func validateTemplate(overlay Template) error {
	if overlay.Version != 1 {
		return fmt.Errorf("unsupported version %d", overlay.Version)
	}
	if strings.TrimSpace(overlay.ID) == "" {
		return fmt.Errorf("id is required")
	}
	seen := map[string]bool{}
	for _, control := range overlay.Controls {
		if strings.TrimSpace(control.ID) == "" {
			return fmt.Errorf("control id is required")
		}
		if seen[control.ID] {
			return fmt.Errorf("duplicate control %q", control.ID)
		}
		seen[control.ID] = true
		switch control.Type {
		case "select":
			if len(control.Choices) == 0 {
				return fmt.Errorf("select control %q requires choices", control.ID)
			}
		case "number", "text", "checkbox", "hidden":
		default:
			return fmt.Errorf("control %q has unsupported type %q", control.ID, control.Type)
		}
		for _, binding := range control.Bindings {
			if err := validatePath(binding.Path); err != nil {
				return fmt.Errorf("control %q: %w", control.ID, err)
			}
		}
	}
	return nil
}

func validatePath(path string) error {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 {
		return fmt.Errorf("request path is required")
	}
	for _, part := range parts {
		if part == "" || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`).MatchString(part) {
			return fmt.Errorf("invalid request path %q", path)
		}
	}
	return nil
}

// Resolve applies automatic matching or one explicitly selected overlay.
func (c Catalog) Resolve(modelID, selection, transport string) Resolved {
	selection = normalizeSelection(selection)
	var matched []Template
	for _, overlay := range c.Templates {
		if overlay.ID == "generic" {
			matched = append(matched, overlay)
			continue
		}
		switch selection {
		case SelectionDefault:
			continue
		case SelectionAuto:
			if overlayMatches(overlay, modelID, transport) {
				matched = append(matched, overlay)
			}
		default:
			if overlay.ID == selection {
				matched = append(matched, overlay)
			}
		}
	}
	controlIndex := map[string]int{}
	resolved := Resolved{}
	for _, overlay := range matched {
		resolved.IDs = append(resolved.IDs, overlay.ID)
		if overlay.ID != "generic" && strings.TrimSpace(overlay.Title) != "" {
			resolved.Title = overlay.Title
		}
		for _, control := range overlay.Controls {
			if idx, ok := controlIndex[control.ID]; ok {
				resolved.Controls[idx] = control
				continue
			}
			controlIndex[control.ID] = len(resolved.Controls)
			resolved.Controls = append(resolved.Controls, control)
		}
	}
	if resolved.Title == "" {
		resolved.Title = "Generic"
	}
	return resolved
}

func normalizeSelection(selection string) string {
	selection = strings.ToLower(strings.TrimSpace(selection))
	switch selection {
	case "", SelectionAuto:
		return SelectionAuto
	case "qwen3.6-preserve-thinking":
		return "qwen3.6"
	case "qwen3.8-preserve-thinking":
		return "qwen3.8"
	default:
		return selection
	}
}

// NormalizeSelection converts legacy preset names to their overlay IDs.
func NormalizeSelection(selection string) string {
	return normalizeSelection(selection)
}

func overlayMatches(overlay Template, modelID, transport string) bool {
	if len(overlay.Match.Transports) > 0 && !containsFold(overlay.Match.Transports, transport) {
		return false
	}
	if len(overlay.Match.ModelIDs) == 0 {
		return false
	}
	for _, pattern := range overlay.Match.ModelIDs {
		if wildcardMatch(pattern, modelID) {
			return true
		}
	}
	return false
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	value = strings.ToLower(strings.TrimSpace(value))
	var expression strings.Builder
	expression.WriteByte('^')
	for _, char := range pattern {
		switch char {
		case '*':
			expression.WriteString(".*")
		case '?':
			expression.WriteByte('.')
		default:
			expression.WriteString(regexp.QuoteMeta(string(char)))
		}
	}
	expression.WriteByte('$')
	matched, _ := regexp.MatchString(expression.String(), value)
	return matched
}

// EffectiveValues merges overlay defaults with saved per-model values.
func (r Resolved) EffectiveValues(saved map[string]any) map[string]any {
	values := make(map[string]any, len(r.Controls))
	declared := make(map[string]bool, len(r.Controls))
	for _, control := range r.Controls {
		declared[control.ID] = true
		if control.Default != nil {
			values[control.ID] = control.Default
		}
	}
	for key, value := range saved {
		if declared[key] {
			values[key] = value
		}
	}
	return values
}

// ValidateValues validates values that are declared by the resolved controls.
func (r Resolved) ValidateValues(values map[string]any) error {
	controls := make(map[string]Control, len(r.Controls))
	for _, control := range r.Controls {
		controls[control.ID] = control
	}
	for key, value := range values {
		control, ok := controls[key]
		if !ok {
			return fmt.Errorf("model overlay does not define option %q", key)
		}
		if value == nil || value == "" {
			continue
		}
		switch control.Type {
		case "select":
			valid := false
			for _, choice := range control.Choices {
				valid = valid || reflect.DeepEqual(normalizeJSONScalar(choice.Value), normalizeJSONScalar(value))
			}
			if !valid {
				return fmt.Errorf("option %q has unsupported value %v", key, value)
			}
		case "number":
			number, ok := numberValue(value)
			if !ok {
				return fmt.Errorf("option %q must be a number", key)
			}
			if control.Min != nil && number < *control.Min {
				return fmt.Errorf("option %q must be at least %v", key, *control.Min)
			}
			if control.Max != nil && number > *control.Max {
				return fmt.Errorf("option %q must be at most %v", key, *control.Max)
			}
		case "text":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("option %q must be text", key)
			}
		case "checkbox":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("option %q must be true or false", key)
			}
		}
	}
	return nil
}

// Apply writes resolved option values into their declared request paths.
func (r Resolved) Apply(body map[string]any, saved map[string]any, transport string) map[string]any {
	if body == nil {
		body = map[string]any{}
	}
	values := r.EffectiveValues(saved)
	for _, control := range r.Controls {
		value, ok := values[control.ID]
		if !ok || value == nil || value == "" {
			continue
		}
		for _, binding := range control.Bindings {
			if len(binding.Transports) > 0 && !containsFold(binding.Transports, transport) {
				continue
			}
			if containsValue(binding.OmitValues, value) {
				continue
			}
			mapped := value
			if len(binding.ValueMap) > 0 {
				var found bool
				mapped, found = binding.ValueMap[fmt.Sprint(value)]
				if !found {
					continue
				}
			}
			setPath(body, binding.Path, mapped)
		}
	}
	return body
}

// BoolValue returns one effective boolean-like overlay value.
func (r Resolved) BoolValue(id string, saved map[string]any) bool {
	value := r.EffectiveValues(saved)[id]
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true") || strings.EqualFold(typed, "enabled")
	default:
		return false
	}
}

func setPath(body map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := body
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func containsValue(values []any, wanted any) bool {
	for _, value := range values {
		if reflect.DeepEqual(normalizeJSONScalar(value), normalizeJSONScalar(wanted)) {
			return true
		}
	}
	return false
}

func normalizeJSONScalar(value any) any {
	if number, ok := numberValue(value); ok {
		return number
	}
	return value
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}
