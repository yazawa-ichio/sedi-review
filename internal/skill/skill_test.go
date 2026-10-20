package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var src = fstest.MapFS{
	"SKILL.md":              {Data: []byte("---\nname: sedi-review\n---\nbody\n")},
	"references/command.md": {Data: []byte("ref\n")},
}

func TestDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, repo := filepath.FromSlash("/h"), filepath.FromSlash("/r")
	cases := []struct{ agent, scope, want string }{
		{"claude", ScopeUser, "/h/.claude/skills"},
		{"codex", ScopeUser, "/h/.agents/skills"},
		{"claude", ScopeProject, "/r/.claude/skills"},
		{"codex", ScopeProject, "/r/.agents/skills"},
	}
	for _, c := range cases {
		got, err := SkillsDir(c.agent, c.scope, home, repo)
		if err != nil || got != filepath.FromSlash(c.want) {
			t.Errorf("%s %s: got %q %v", c.agent, c.scope, got, err)
		}
	}
	if _, err := SkillsDir("cursor", ScopeUser, home, repo); err == nil {
		t.Error("unknown agent should fail")
	}
	if _, err := SkillsDir("claude", ScopeProject, home, ""); err == nil {
		t.Error("project scope without a repository should fail")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.FromSlash("/c"))
	if got, _ := SkillsDir("claude", ScopeUser, home, repo); got != filepath.FromSlash("/c/skills") {
		t.Errorf("CLAUDE_CONFIG_DIR should be respected, got %q", got)
	}
}

func TestInstallFlow(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "sedi-review")
	if st, _ := Status(src, dest); st != NotInstalled {
		t.Fatalf("status before install: %s", st)
	}
	if msg, err := Install(src, dest, false); err != nil || !strings.HasPrefix(msg, "入れました") {
		t.Fatalf("install: %q %v", msg, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "references", "command.md")); string(data) != "ref\n" {
		t.Error("nested files should be copied")
	}
	if st, _ := Status(src, dest); st != UpToDate {
		t.Errorf("status after install: %s", st)
	}
	if msg, err := Install(src, dest, false); err != nil || !strings.HasPrefix(msg, "既に同じ版") {
		t.Errorf("second install: %q %v", msg, err)
	}

	// 手で直したものは、force が無ければ置き換えない。
	os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("edited"), 0o644)
	if st, _ := Status(src, dest); st != Differs {
		t.Errorf("status after edit: %s", st)
	}
	if _, err := Install(src, dest, false); err == nil {
		t.Error("install over an edited skill should need --force")
	}
	if _, err := Uninstall(src, dest, false); err == nil {
		t.Error("uninstall of an edited skill should need --force")
	}
	if msg, err := Install(src, dest, true); err != nil || !strings.HasPrefix(msg, "置き換えました") {
		t.Errorf("forced install: %q %v", msg, err)
	}

	// 利用者が足したファイルは消さない。
	extra := filepath.Join(dest, "notes.md")
	os.WriteFile(extra, []byte("mine"), 0o644)
	if msg, err := Uninstall(src, dest, false); err != nil || !strings.Contains(msg, "残っています") {
		t.Errorf("uninstall with an extra file: %q %v", msg, err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Error("a file the user added must be kept")
	}
	if _, err := os.Stat(filepath.Join(dest, "references")); err == nil {
		t.Error("an emptied folder should be removed")
	}
	os.Remove(extra)
	if msg, _ := Uninstall(src, dest, false); !strings.HasPrefix(msg, "入っていません") {
		t.Errorf("uninstall when not installed: %q", msg)
	}
}

func TestUninstallRemovesFolder(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "sedi-review")
	if _, err := Install(src, dest, false); err != nil {
		t.Fatal(err)
	}
	if msg, err := Uninstall(src, dest, false); err != nil || !strings.HasPrefix(msg, "消しました") {
		t.Fatalf("uninstall: %q %v", msg, err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("the skill folder should be gone")
	}
}

func TestNames(t *testing.T) {
	root := fstest.MapFS{
		"sedi-review/SKILL.md":      {Data: []byte("a")},
		"sedi-review-init/SKILL.md": {Data: []byte("b")},
		"notes/readme.md":           {Data: []byte("c")},
	}
	got, err := Names(root)
	if err != nil || strings.Join(got, ",") != "sedi-review,sedi-review-init" {
		t.Errorf("Names: %v %v", got, err)
	}
}
