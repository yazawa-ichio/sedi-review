package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yazawa-ichio/sedi-review/internal/store"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(prefix + strings.Repeat("-", i) + "\n")
	}
	return b.String()
}

// setup は 2 つのブランチを持つ一時リポジトリを作り、Sedi Review の初期化までする。
func setup(t *testing.T) string {
	t.Helper()
	dir := setupRepo(t)
	if _, err := Init(dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "core.autocrlf", "false")
	write(t, dir, "a.cs", lines("a", 10))
	write(t, dir, "b.cs", lines("b", 10))
	write(t, dir, "b.cs.meta", "guid: 1\n")
	write(t, dir, "c.txt", lines("c", 10))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "work")
	write(t, dir, "a.cs", strings.Replace(lines("a", 10), "a--\n", "A2\n", 1))
	write(t, dir, "b.cs", strings.Replace(lines("b", 10), "b---\n", "B3\n", 1))
	write(t, dir, "b.cs.meta", "guid: 2\n")
	write(t, dir, "c.txt", strings.Replace(lines("c", 10), "c-\n", "C1\n", 1))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "work")
	return dir
}

func firstHunk(t *testing.T, s *State, path string) string {
	t.Helper()
	f := s.File(path)
	if f == nil || len(f.Hunks) == 0 {
		t.Fatalf("%s has no hunks", path)
	}
	return f.Hunks[0].ID
}

func TestReviewFlow(t *testing.T) {
	dir := setup(t)
	s, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.File("b.cs.meta") != nil {
		t.Error("the unity rule should exclude *.cs.meta")
	}
	if s.File("a.cs") == nil || s.File("b.cs") == nil || s.File("c.txt") == nil {
		t.Fatalf("missing files: %v", s.Files)
	}

	if err := s.SetSections([]byte(`[{"name":"x","files":["missing.cs"]}]`)); err == nil {
		t.Error("unknown path should be rejected")
	}
	if err := s.SetSections([]byte(`[{"name":"a / b"}]`)); err == nil {
		t.Error("separator in a name should be rejected")
	}
	hb := firstHunk(t, s, "b.cs")
	secs := `[{"name":"A","files":["a.cs"],"hunks":["` + hb + `"],"children":[{"name":"child","hunks":["` + hb + `"]}]},{"name":"C","files":["c.txt"]}]`
	if err := s.SetSections([]byte(secs)); err != nil {
		t.Fatal(err)
	}
	if got := s.HunkSections(hb); len(got) != 2 {
		t.Errorf("hunk should belong to two sections, got %v", got)
	}
	if un, _ := s.Unassigned(); len(un) != 0 {
		t.Errorf("all hunks are assigned, got unassigned %v", un)
	}

	if err := s.SetMark("file", "a.cs", true, store.AuthorAgent); err == nil {
		t.Error("the agent must not set marks")
	}
	for _, m := range [][2]string{{"file", "a.cs"}, {"file", "c.txt"}, {"section", "A"}, {"section", "C"}} {
		if err := s.SetMark(m[0], m[1], true, store.AuthorWorker); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetMark("file", "c.txt", false, store.AuthorAgent); err != nil {
		t.Fatal(err)
	}
	if s.FileMarked("c.txt") {
		t.Error("the agent may remove marks")
	}

	ha := firstHunk(t, s, "a.cs")
	line, err := s.ParseAnchor("line:" + ha + ":new:0-1")
	if err != nil {
		t.Fatal(err)
	}
	th, err := s.NewThread("question", line, "why?", store.AuthorWorker)
	if err != nil {
		t.Fatal(err)
	}
	if th.State != store.StateUnsent || len(th.Anchor.Text) != 2 {
		t.Errorf("new thread: %+v", th)
	}
	if _, err := s.EditMessage(th.ID, th.Messages[0].ID, "why this?"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSent([]*store.Thread{th}); err != nil {
		t.Fatal(err)
	}
	if th.State != store.StateSent {
		t.Errorf("state after send: %s", th.State)
	}
	if _, err := s.EditMessage(th.ID, th.Messages[0].ID, "x"); err == nil {
		t.Error("sent messages must not be editable")
	}
	if _, err := s.AddMessage(th.ID, "because", store.AuthorAgent); err != nil {
		t.Fatal(err)
	}
	if th.State != store.StateReplied {
		t.Errorf("state after reply: %s", th.State)
	}
	prompt, path, err := s.ExportAll()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, filepath.ToSlash(path)) {
		t.Errorf("export prompt should name the file: %s", prompt)
	}

	// 作業ブランチが進む: a.cs は変わり、c.txt は変わらない。
	write(t, dir, "a.cs", strings.Replace(lines("a", 10), "a--\n", "A2-changed\n", 1))
	git(t, dir, "commit", "-q", "-am", "more")
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if s.FileMarked("a.cs") {
		t.Error("a changed file should lose its mark on refresh")
	}
	if s.SectionMarked("A") {
		t.Error("a section whose hunk changed should lose its mark")
	}
	if !s.SectionMarked("C") {
		t.Error("an unchanged section should keep its mark")
	}
	if _, ok := s.Describe(th.Anchor); ok {
		t.Error("the line anchor should be orphaned after its hunk changed")
	}
	newLine, err := s.ParseAnchor("line:" + firstHunk(t, s, "a.cs") + ":new:0-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reattach(th.ID, newLine); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Describe(th.Anchor); !ok {
		t.Error("reattached anchor should resolve")
	}

	// 保存したものから開き直しても同じ状態になる。
	again, err := Load(dir, s.Review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.SectionMarked("C") || again.FileMarked("a.cs") || len(again.Threads) != 1 {
		t.Errorf("reloaded state differs")
	}
}

func TestDeleteLastUnsentRemovesThread(t *testing.T) {
	dir := setup(t)
	s, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	th, err := s.NewThread("other", store.Anchor{Type: store.AnchorReview}, "note", store.AuthorWorker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMessage(th.ID, th.Messages[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Threads) != 0 {
		t.Error("thread should be removed")
	}
}

func TestOpenNeedsInit(t *testing.T) {
	dir := setupRepo(t)
	if _, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true}); err == nil || !strings.Contains(err.Error(), "init") {
		t.Fatalf("opening before init should fail with a hint, got %v", err)
	}
}

func TestInit(t *testing.T) {
	dir := setupRepo(t)
	if _, err := Init(dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Base != "main" || cfg.Rule != "unity" || cfg.TwoDot {
		t.Errorf("default config: %+v", cfg)
	}

	if _, err := Init(dir, InitOptions{Base: "work"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadConfig(dir); cfg.Base != "main" {
		t.Error("init must not overwrite an existing config without --force")
	}
	if _, err := Init(dir, InitOptions{Base: "work", Force: true}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadConfig(dir); cfg.Base != "work" {
		t.Error("init --force should rewrite the config")
	}
	if _, err := Init(dir, InitOptions{Rule: "nope", Force: true}); err == nil {
		t.Error("an unknown rule should be rejected")
	}

	// レビューを作っても、git から見えるのは追跡する設定のファイルだけ。
	s, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewThread("other", store.Anchor{Type: store.AnchorReview}, "note", store.AuthorWorker); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ExportAll(); err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "status", "--porcelain", "--untracked-files=all"); got != "?? "+ConfigFile {
		t.Errorf("git status should only show the config file, got:\n%s", got)
	}
	if !strings.HasPrefix(filepath.ToSlash(s.Dir), filepath.ToSlash(filepath.Join(dir, ".sedi-review", "reviews"))) {
		t.Errorf("review data should live in the repository: %s", s.Dir)
	}
}

func TestReviewsDoNotCollideAcrossRepos(t *testing.T) {
	a, b := setup(t), setup(t)
	sa, err := Create(store.Spec{Repo: a, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := Create(store.Spec{Repo: b, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	if sa.Dir == sb.Dir {
		t.Fatal("two repositories share a review folder")
	}
	if _, err := sa.NewThread("other", store.Anchor{Type: store.AnchorReview}, "only in a", store.AuthorWorker); err != nil {
		t.Fatal(err)
	}
	again, err := Load(b, sb.Review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Threads) != 0 {
		t.Error("a message in one repository leaked into another")
	}
}
