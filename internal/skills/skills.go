package skills

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/agents"
	"gopkg.in/yaml.v3"
)

const (
	fileName               = "SKILL.md"
	DefaultCatalogMaxChars = 12_000
	maxSkillNameLen        = 64
	maxDescriptionLen      = 1024
	maxCompatibilityLen    = 500
	maxCatalogDescription  = 512
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
	ScopeManaged Scope = "managed"
)

// Presentation contains optional, host-neutral UI hints stored in the
// SKILL.md metadata map. Agents that do not understand them safely ignore them.
type Presentation struct {
	DisplayName      string
	ShortDescription string
	Logo             string
	LogoPath         string
	BrandColor       string
}

type Skill struct {
	Name               string
	Description        string
	License            string
	Compatibility      string
	Metadata           map[string]string
	Presentation       Presentation
	Path               string
	CanonicalPath      string
	Directory          string
	CanonicalDirectory string
	Scope              Scope
	Root               string
	Enabled            bool
	Valid              bool
	Effective          bool
	ShadowedBy         string
	Error              string
	Warnings           []string
}

type Root struct {
	Path   string
	Scope  Scope
	Exists bool
}

type Catalog struct {
	Items []Skill
	Roots []Root
}

type DiscoverOptions struct {
	ProjectRoot     string
	UserRoots       []string
	ManagedRoots    []string
	DisabledPaths   []string
	CatalogMaxChars int
}

type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
}

func Discover(workdir string) []Skill {
	return DiscoverWithOptions(workdir, DiscoverOptions{})
}

func DiscoverWithOptions(workdir string, opts DiscoverOptions) []Skill {
	catalog := InspectWithOptions(workdir, opts)
	out := make([]Skill, 0, len(catalog.Items))
	for _, skill := range catalog.Items {
		if skill.Valid && skill.Enabled && skill.Effective {
			out = append(out, skill)
		}
	}
	return out
}

// InspectWithOptions returns every discovered skill, including invalid,
// disabled, and shadowed entries. Root order is precedence order.
func InspectWithOptions(workdir string, opts DiscoverOptions) Catalog {
	roots := discoveryRoots(workdir, opts)
	disabled := normalizedPathSet(opts.DisabledPaths)
	seenFiles := map[string]struct{}{}
	items := make([]Skill, 0)

	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, entry := range entries {
			dir := filepath.Join(root.Path, entry.Name())
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			skillPath := filepath.Join(dir, fileName)
			if info, err = os.Stat(skillPath); err != nil || info.IsDir() {
				continue
			}
			canonicalPath := canonicalPath(skillPath)
			if _, exists := seenFiles[canonicalPath]; exists {
				continue
			}
			seenFiles[canonicalPath] = struct{}{}
			skill, parseErr := loadSkill(skillPath, root.Scope, root.Path, entry.Name())
			skill.Enabled = !pathDisabled(skill, disabled)
			if parseErr != nil {
				skill.Error = parseErr.Error()
			}
			items = append(items, skill)
		}
	}

	effective := map[string]string{}
	for idx := range items {
		item := &items[idx]
		if !item.Valid || !item.Enabled {
			continue
		}
		key := normalizeName(item.Name)
		if winner, exists := effective[key]; exists {
			item.ShadowedBy = winner
			continue
		}
		item.Effective = true
		effective[key] = item.CanonicalPath
	}

	return Catalog{Items: items, Roots: roots}
}

// InspectFile parses and validates one SKILL.md file or skill directory.
func InspectFile(path string) (Skill, error) {
	path = cleanPath(path)
	info, err := os.Stat(path)
	if err != nil {
		return Skill{}, err
	}
	if info.IsDir() {
		path = filepath.Join(path, fileName)
	}
	if filepath.Base(path) != fileName {
		return Skill{}, fmt.Errorf("skill entrypoint must be named %s", fileName)
	}
	return loadSkill(path, ScopeProject, filepath.Dir(filepath.Dir(path)), filepath.Base(filepath.Dir(path)))
}

func Find(workdir string, name string) (Skill, bool) {
	return FindWithOptions(workdir, name, DiscoverOptions{})
}

func FindWithOptions(workdir string, name string, opts DiscoverOptions) (Skill, bool) {
	needle := normalizeName(name)
	for _, skill := range DiscoverWithOptions(workdir, opts) {
		if normalizeName(skill.Name) == needle {
			return skill, true
		}
	}
	return Skill{}, false
}

func AvailableNames(workdir string) []string {
	return AvailableNamesWithOptions(workdir, DiscoverOptions{})
}

func AvailableNamesWithOptions(workdir string, opts DiscoverOptions) []string {
	items := DiscoverWithOptions(workdir, opts)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func ToolDescription(base string, workdir string) string {
	return ToolDescriptionWithOptions(base, workdir, DiscoverOptions{})
}

func ToolDescriptionWithOptions(base string, workdir string, opts DiscoverOptions) string {
	listing := catalogXML(DiscoverWithOptions(workdir, opts), catalogLimit(opts))
	if listing == "" {
		return base
	}
	return strings.TrimSpace(base) + "\n\n" + listing
}

func PromptContext(workdir string) string {
	return PromptContextWithOptions(workdir, DiscoverOptions{})
}

func PromptContextWithOptions(workdir string, opts DiscoverOptions) string {
	listing := catalogXML(DiscoverWithOptions(workdir, opts), catalogLimit(opts))
	if listing == "" {
		return ""
	}
	return "Available skills:\n" +
		"- Skills are reusable local workflows and are lazy-loaded.\n" +
		"- Load a skill when the request clearly matches its description.\n" +
		"- Users can explicitly request a skill with $skill-name; always load that listed skill.\n" +
		"- After loading a skill, follow its instructions and read only the referenced resources needed for the task.\n" +
		"- Never guess an unlisted skill name.\n" + listing
}

func catalogXML(items []Skill, maxChars int) string {
	if len(items) == 0 {
		return ""
	}
	const open = "<available_skills>"
	const close = "\n</available_skills>"
	var b strings.Builder
	b.WriteString(open)
	omitted := 0
	for _, item := range items {
		description := truncate(item.Description, maxCatalogDescription)
		entry := "\n<skill>\n<name>" + html.EscapeString(item.Name) + "</name>"
		if description != "" {
			entry += "\n<description>" + html.EscapeString(description) + "</description>"
		}
		entry += "\n</skill>"
		if b.Len()+len(entry)+len(close) > maxChars {
			omitted++
			continue
		}
		b.WriteString(entry)
	}
	if omitted > 0 {
		notice := fmt.Sprintf("\n<omitted count=\"%d\">Additional skills omitted by catalog budget.</omitted>", omitted)
		if b.Len()+len(notice)+len(close) <= maxChars {
			b.WriteString(notice)
		}
	}
	b.WriteString(close)
	return b.String()
}

func discoveryRoots(workdir string, opts DiscoverOptions) []Root {
	projectRoot := agents.NormalizeProjectRoot(opts.ProjectRoot)
	if strings.TrimSpace(opts.ProjectRoot) == "" {
		projectRoot = agents.NormalizeProjectRoot(workdir)
	}
	roots := projectRoots(workdir, projectRoot)
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots,
			Root{Path: filepath.Join(home, ".agents", "skills"), Scope: ScopeUser},
			Root{Path: filepath.Join(home, ".koder", "skills"), Scope: ScopeManaged},
		)
	}
	for _, root := range opts.UserRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, Root{Path: root, Scope: ScopeUser})
		}
	}
	for _, root := range opts.ManagedRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, Root{Path: root, Scope: ScopeManaged})
		}
	}

	seen := map[string]struct{}{}
	out := make([]Root, 0, len(roots))
	for _, root := range roots {
		root.Path = cleanPath(root.Path)
		key := canonicalPath(root.Path)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		info, err := os.Stat(root.Path)
		root.Exists = err == nil && info.IsDir()
		out = append(out, root)
	}
	return out
}

func projectRoots(workdir string, projectRoot string) []Root {
	var roots []Root
	current := cleanPath(workdir)
	projectRoot = cleanPath(projectRoot)
	if current == "" {
		current = projectRoot
	}
	if projectRoot == "" {
		projectRoot = current
	}
	if current == "" {
		return roots
	}
	for {
		roots = append(roots, Root{Path: filepath.Join(current, ".agents", "skills"), Scope: ScopeProject})
		if current == projectRoot {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func loadSkill(path string, scope Scope, root string, fallbackName string) (Skill, error) {
	skill := Skill{
		Name:      normalizeName(fallbackName),
		Path:      cleanPath(path),
		Directory: cleanPath(filepath.Dir(path)),
		Scope:     scope,
		Root:      cleanPath(root),
		Enabled:   true,
		Metadata:  map[string]string{},
	}
	skill.CanonicalPath = canonicalPath(skill.Path)
	skill.CanonicalDirectory = canonicalPath(skill.Directory)

	body, err := os.ReadFile(skill.Path)
	if err != nil {
		return skill, fmt.Errorf("read %s: %w", fileName, err)
	}
	frontmatter, err := parseFrontmatter(body)
	if err != nil {
		return skill, err
	}
	skill.Name = strings.TrimSpace(frontmatter.Name)
	skill.Description = collapseWhitespace(frontmatter.Description)
	skill.License = strings.TrimSpace(frontmatter.License)
	skill.Compatibility = collapseWhitespace(frontmatter.Compatibility)
	skill.Metadata = cloneMetadata(frontmatter.Metadata)
	if err := validateMetadata(skill, fallbackName); err != nil {
		return skill, err
	}
	skill.Presentation, skill.Warnings = presentationForSkill(skill)
	skill.Valid = true
	return skill, nil
}

func parseFrontmatter(body []byte) (skillFrontmatter, error) {
	text := strings.TrimPrefix(string(body), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return skillFrontmatter{}, errors.New("missing YAML frontmatter delimited by ---")
	}
	closing := -1
	for idx := 1; idx < len(lines); idx++ {
		if strings.TrimSpace(lines[idx]) == "---" {
			closing = idx
			break
		}
	}
	if closing < 0 {
		return skillFrontmatter{}, errors.New("YAML frontmatter is not closed with ---")
	}
	var metadata skillFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closing], "\n")), &metadata); err != nil {
		return skillFrontmatter{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	return metadata, nil
}

func validateMetadata(skill Skill, directoryName string) error {
	if skill.Name == "" {
		return errors.New("missing name in YAML frontmatter")
	}
	if utf8.RuneCountInString(skill.Name) > maxSkillNameLen || !skillNamePattern.MatchString(skill.Name) {
		return fmt.Errorf("name %q must use 1-%d lowercase letters, digits, and single hyphens", skill.Name, maxSkillNameLen)
	}
	if skill.Name != directoryName {
		return fmt.Errorf("name %q must match directory %q", skill.Name, directoryName)
	}
	if skill.Description == "" {
		return errors.New("missing description in YAML frontmatter")
	}
	if utf8.RuneCountInString(skill.Description) > maxDescriptionLen {
		return fmt.Errorf("description is longer than %d characters", maxDescriptionLen)
	}
	if utf8.RuneCountInString(skill.Compatibility) > maxCompatibilityLen {
		return fmt.Errorf("compatibility is longer than %d characters", maxCompatibilityLen)
	}
	return nil
}

func presentationForSkill(skill Skill) (Presentation, []string) {
	metadata := skill.Metadata
	presentation := Presentation{
		DisplayName:      metadataValue(metadata, "display-name", "display_name", "title"),
		ShortDescription: collapseWhitespace(metadataValue(metadata, "short-description", "short_description")),
		BrandColor:       strings.TrimSpace(metadataValue(metadata, "brand-color", "brand_color")),
		Logo:             strings.TrimSpace(metadataValue(metadata, "logo", "logo-small", "logo_small", "icon")),
	}
	if presentation.DisplayName == "" {
		presentation.DisplayName = humanizeName(skill.Name)
	}
	if presentation.ShortDescription == "" {
		presentation.ShortDescription = skill.Description
	}
	var warnings []string
	if presentation.BrandColor != "" && !validBrandColor(presentation.BrandColor) {
		warnings = append(warnings, "metadata brand-color must be a #RGB, #RRGGBB, or #RRGGBBAA color")
		presentation.BrandColor = ""
	}
	logo, err := resolveLogo(skill, presentation.Logo)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else if logo != "" {
		presentation.Logo = relativeSlash(skill.Directory, logo)
		presentation.LogoPath = logo
	}
	return presentation, warnings
}

func resolveLogo(skill Skill, configured string) (string, error) {
	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
	} else {
		candidates = append(candidates,
			"assets/logo.png", "assets/logo.webp", "assets/logo.jpg", "assets/logo.jpeg", "assets/logo.gif",
			"assets/icon.png", "assets/icon.webp", "assets/icon.jpg", "assets/icon.jpeg", "assets/icon.gif",
		)
	}
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) || escapesDirectory(candidate) {
			if configured != "" {
				return "", errors.New("metadata logo must be a relative path inside the skill directory")
			}
			continue
		}
		path := filepath.Join(skill.Directory, filepath.FromSlash(candidate))
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			if configured != "" {
				return "", fmt.Errorf("metadata logo %q does not exist", configured)
			}
			continue
		}
		canonical := canonicalPath(path)
		rel, err := filepath.Rel(skill.CanonicalDirectory, canonical)
		if err != nil || escapesDirectory(rel) {
			return "", errors.New("metadata logo resolves outside the skill directory")
		}
		switch strings.ToLower(filepath.Ext(canonical)) {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif":
			return canonical, nil
		default:
			return "", errors.New("skill logos must be PNG, JPEG, WebP, or GIF")
		}
	}
	return "", nil
}

func normalizedPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths)*2)
	for _, path := range paths {
		if path = strings.TrimSpace(path); path == "" {
			continue
		}
		clean := cleanPath(path)
		out[clean] = struct{}{}
		out[canonicalPath(clean)] = struct{}{}
	}
	return out
}

func pathDisabled(skill Skill, disabled map[string]struct{}) bool {
	for _, path := range []string{skill.Path, skill.CanonicalPath, skill.Directory, skill.CanonicalDirectory} {
		if _, exists := disabled[path]; exists {
			return true
		}
	}
	return false
}

func metadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func cloneMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func canonicalPath(path string) string {
	path = cleanPath(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return cleanPath(resolved)
}

func cleanPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func escapesDirectory(path string) bool {
	path = filepath.Clean(path)
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func relativeSlash(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	return filepath.ToSlash(rel)
}

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

func humanizeName(name string) string {
	words := strings.Fields(strings.ReplaceAll(name, "-", " "))
	for idx, word := range words {
		if word != "" {
			words[idx] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func validBrandColor(value string) bool {
	if !strings.HasPrefix(value, "#") {
		return false
	}
	digits := value[1:]
	if len(digits) != 3 && len(digits) != 6 && len(digits) != 8 {
		return false
	}
	for _, char := range digits {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

func catalogLimit(opts DiscoverOptions) int {
	if opts.CatalogMaxChars <= 0 {
		return DefaultCatalogMaxChars
	}
	return max(opts.CatalogMaxChars, 512)
}

func truncate(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maxLen || maxLen <= 3 {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxLen-3])) + "..."
}

func DebugString(items []Skill) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s:%s", item.Scope, item.Name))
	}
	return strings.Join(parts, ", ")
}
