package permissionprofile

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

const (
	ProfileAsk        = "ask"
	ProfileReadAsk    = "read-ask"
	ProfileWriteAsk   = "write-ask"
	ProfileFullAccess = "full-access"

	ModeReadOnly  MountMode = "readonly"
	ModeReadWrite MountMode = "readwrite"
)

// Rules is the configured permission profile set plus active profile name.
type Rules struct {
	Profile  string             `toml:"profile"`
	Profiles map[string]Profile `toml:"profiles"`
}

// Profile is a named sandbox policy, with legacy rules accepted for migration.
type Profile struct {
	Network   bool    `toml:"network"`
	Root      string  `toml:"root"`
	Workspace string  `toml:"workspace"`
	Mounts    []Mount `toml:"mounts"`
	Rules     []Rule  `toml:"rules,omitempty"`
}

// Rule grants, asks, or denies a tool matching a pattern.
type Rule struct {
	Tool    domain.ToolKind       `toml:"tool"`
	Pattern string                `toml:"pattern"`
	Action  domain.PermissionMode `toml:"action"`
}

// MountMode controls whether a bwrap bind mount is read-only or read-write.
type MountMode string

// Mount is an extra host folder exposed inside the sandbox.
type Mount struct {
	Path string    `toml:"path" json:"path"`
	Mode MountMode `toml:"mode" json:"mode"`
}

// AccessKind is a coarse permission category supplied by the tool caller.
type AccessKind string

const (
	AccessUnknown AccessKind = ""
	AccessRead    AccessKind = "read"
	AccessWrite   AccessKind = "write"
	AccessShell   AccessKind = "shell"
)

type ProfileOption struct {
	Name        string
	Label       string
	Description string
}

// Request describes the permission-sensitive operation being evaluated.
type Request struct {
	Tool           domain.ToolKind
	Access         AccessKind
	Pattern        string
	ProjectRoot    string
	Targets        []string
	OutsideProject bool
	Ambiguous      bool
}

// Decision is the outcome of evaluating a permission request.
type Decision struct {
	Mode   domain.PermissionMode
	Reason string
}

// Evaluate returns the permission decision for req under profileName.
func Evaluate(cfg Rules, profileName string, overrides []domain.PermissionOverride, req Request) Decision {
	pattern := req.Pattern
	if pattern == "" {
		pattern = "*"
	}
	for _, rule := range slices.Backward(overrides) {
		if !toolMatches(rule.Tool, req.Tool) {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return Decision{Mode: rule.Action}
		}
	}
	if IsBuiltinProfile(profileName) {
		return evaluateBuiltin(profileName, req)
	}
	profile, ok := cfg.Profiles[profileName]
	if !ok {
		profile = cfg.Profiles[cfg.Profile]
	}
	if len(profile.Rules) == 0 {
		return Decision{Mode: domain.PermissionModeAllow}
	}

	for _, rule := range slices.Backward(profile.Rules) {
		if !toolMatches(rule.Tool, req.Tool) {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return Decision{Mode: rule.Action}
		}
	}
	return Decision{Mode: domain.PermissionModeDeny}
}

// Normalize fills missing sandbox fields with conservative defaults.
func Normalize(profile Profile) Profile {
	if !validMountMode(MountMode(profile.Root)) {
		profile.Root = string(ModeReadOnly)
	}
	if !validMountMode(MountMode(profile.Workspace)) {
		profile.Workspace = string(ModeReadWrite)
	}
	for idx := range profile.Mounts {
		if !validMountMode(profile.Mounts[idx].Mode) {
			profile.Mounts[idx].Mode = ModeReadOnly
		}
	}
	return profile
}

// ValidateSandbox reports whether profile contains supported sandbox settings.
func ValidateSandbox(profile Profile) error {
	if !validMountMode(MountMode(profile.Root)) {
		return fmt.Errorf("invalid root mode %q", profile.Root)
	}
	if !validMountMode(MountMode(profile.Workspace)) {
		return fmt.Errorf("invalid workspace mode %q", profile.Workspace)
	}
	for _, mount := range profile.Mounts {
		if strings.TrimSpace(mount.Path) == "" {
			return fmt.Errorf("mount path is required")
		}
		if !filepath.IsAbs(strings.TrimSpace(mount.Path)) {
			return fmt.Errorf("mount path %q must be absolute", mount.Path)
		}
		if !validMountMode(mount.Mode) {
			return fmt.Errorf("invalid mount mode %q", mount.Mode)
		}
	}
	return nil
}

// BuiltinProfiles returns the built-in permission profiles in display order.
func BuiltinProfiles() []ProfileOption {
	return []ProfileOption{
		{Name: ProfileFullAccess, Label: "full access", Description: "Network on, root readwrite, workspace readwrite"},
	}
}

// Description returns a concise description for a permission profile.
func Description(name string, cfg Rules) string {
	name = strings.TrimSpace(name)
	for _, item := range BuiltinProfiles() {
		if item.Name == name {
			return item.Description
		}
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return ""
	}
	profile = Normalize(profile)
	if len(profile.Rules) == 0 {
		network := "network off"
		if profile.Network {
			network = "network on"
		}
		return fmt.Sprintf("%s, root %s, workspace %s", network, profile.Root, profile.Workspace)
	}
	return summarizeRules(profile.Rules)
}

// IsBuiltinProfile reports whether name is a built-in profile.
func IsBuiltinProfile(name string) bool {
	switch strings.TrimSpace(name) {
	case ProfileFullAccess:
		return true
	default:
		return false
	}
}

// DisplayName returns a short label for a permission profile.
func DisplayName(name string) string {
	for _, item := range BuiltinProfiles() {
		if item.Name == strings.TrimSpace(name) {
			return item.Label
		}
	}
	if strings.TrimSpace(name) == "" {
		return "-"
	}
	return name
}

// ProfileNames returns built-in and configured profile names in display order.
func ProfileNames(cfg Rules) []string {
	names := make([]string, 0, len(cfg.Profiles)+len(BuiltinProfiles()))
	seen := map[string]struct{}{}
	var configured []string
	for name := range cfg.Profiles {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		configured = append(configured, name)
		seen[name] = struct{}{}
	}
	sort.Strings(configured)
	names = append(names, configured...)
	for _, item := range BuiltinProfiles() {
		if _, ok := seen[item.Name]; ok {
			continue
		}
		names = append(names, item.Name)
	}
	return names
}

// Validate reports whether mode is a supported permission mode.
func Validate(mode domain.PermissionMode) error {
	switch mode {
	case domain.PermissionModeAllow, domain.PermissionModeAsk, domain.PermissionModeDeny:
		return nil
	default:
		return fmt.Errorf("invalid permission mode %q", mode)
	}
}

func validMountMode(mode MountMode) bool {
	switch mode {
	case ModeReadOnly, ModeReadWrite:
		return true
	default:
		return false
	}
}

func wildcardMatch(pattern, value string) bool {
	if pattern == "" {
		pattern = "*"
	}
	if pattern == "*" || pattern == "**" {
		return true
	}
	var expr strings.Builder
	expr.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			expr.WriteString(".*")
		case '?':
			expr.WriteString(".")
		default:
			expr.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	expr.WriteString("$")
	matched, err := regexp.MatchString(expr.String(), value)
	if err != nil {
		return pattern == value
	}
	return matched
}

func evaluateBuiltin(profileName string, req Request) Decision {
	switch profileName {
	case ProfileAsk:
		return Decision{Mode: domain.PermissionModeAsk, Reason: "this mode requires approval for all tool actions"}
	case ProfileReadAsk:
		if req.Access == AccessShell {
			return Decision{Mode: domain.PermissionModeAsk, Reason: "shell commands require approval in this mode"}
		}
		if req.Access == AccessRead && req.targetsProjectOnly() {
			return Decision{Mode: domain.PermissionModeAllow}
		}
		return Decision{Mode: domain.PermissionModeAsk, Reason: req.reason("this mode only auto-allows reads in the current project")}
	case ProfileWriteAsk:
		if req.Access == AccessShell {
			return Decision{Mode: domain.PermissionModeAsk, Reason: "shell commands require approval in this mode"}
		}
		if (req.Access == AccessRead || req.Access == AccessWrite) && req.targetsProjectOnly() {
			return Decision{Mode: domain.PermissionModeAllow}
		}
		return Decision{Mode: domain.PermissionModeAsk, Reason: req.reason("this mode only auto-allows reads and writes in the current project")}
	case ProfileFullAccess:
		return Decision{Mode: domain.PermissionModeAllow}
	default:
		return Decision{Mode: domain.PermissionModeDeny}
	}
}

func summarizeRules(rules []Rule) string {
	if len(rules) == 0 {
		return ""
	}
	counts := map[domain.PermissionMode]int{}
	for _, rule := range rules {
		counts[rule.Action]++
	}
	parts := make([]string, 0, 3)
	for _, mode := range []domain.PermissionMode{domain.PermissionModeAllow, domain.PermissionModeAsk, domain.PermissionModeDeny} {
		if counts[mode] == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[mode], mode))
	}
	return strings.Join(parts, ", ")
}

func (req Request) targetsProjectOnly() bool {
	if strings.TrimSpace(req.ProjectRoot) == "" {
		return false
	}
	if req.Ambiguous || req.OutsideProject {
		return false
	}
	if len(req.Targets) == 0 {
		return false
	}
	projectRoot := filepath.Clean(req.ProjectRoot)
	for _, target := range req.Targets {
		if strings.TrimSpace(target) == "" {
			return false
		}
		target = filepath.Clean(target)
		rel, err := filepath.Rel(projectRoot, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func toolMatches(ruleTool, reqTool domain.ToolKind) bool {
	if strings.TrimSpace(string(ruleTool)) == "" {
		return false
	}
	return wildcardMatch(string(ruleTool), string(reqTool))
}

func (req Request) reason(fallback string) string {
	switch {
	case req.OutsideProject:
		return "target is outside the current project folder"
	case req.Ambiguous:
		return "request target could not be classified relative to the current project folder"
	default:
		return fallback
	}
}
