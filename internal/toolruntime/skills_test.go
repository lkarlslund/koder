package toolruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestWithLoadedSkillMountsGrantsOnlySkillDirectoryReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "portable")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: portable\ndescription: Portable test workflow\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tools.SkillStoredResult{Name: "portable", Path: skillPath})
	if err != nil {
		t.Fatal(err)
	}
	timeline := []domain.TimelineItem{{Content: domain.AssistantMessage{Tools: []domain.ToolCall{{
		Tool:   domain.ToolKindSkill,
		Result: &domain.ToolResult{Data: json.RawMessage(raw)},
	}}}}}

	got := withLoadedSkillMounts(accesssettings.Settings{}, timeline)
	if len(got.Mounts) != 1 || got.Mounts[0].Path != dir || got.Mounts[0].Mode != accesssettings.ModeReadOnly {
		t.Fatalf("unexpected loaded skill mounts: %#v", got.Mounts)
	}
}

func TestWithLoadedSkillMountsRejectsArbitraryStoredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-skill.txt")
	if err := os.WriteFile(path, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	timeline := []domain.TimelineItem{{Content: domain.ToolExecution{
		Tool:   domain.ToolKindSkill,
		Result: &domain.ToolResult{Data: tools.SkillStoredResult{Name: "fake", Path: path}},
	}}}
	got := withLoadedSkillMounts(accesssettings.Settings{}, timeline)
	if len(got.Mounts) != 0 {
		t.Fatalf("arbitrary stored path gained access: %#v", got.Mounts)
	}
}
