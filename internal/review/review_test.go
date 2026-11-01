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

func TestSectionLineRanges(t *testing.T) {
	dir := setupRepo(t)
	write(t, dir, "n.cs", lines("n", 30))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "new file")
	if _, err := Init(dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	s, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	nf := s.File("n.cs")
	if nf == nil || len(nf.Hunks) != 1 {
		t.Fatalf("n.cs should be one hunk: %+v", nf)
	}
	nh := nf.Hunks[0].ID

	bad := []string{
		`[{"name":"x","lines":[{"path":"n.cs","from":100,"to":200}]}]`,
		`[{"name":"x","lines":[{"path":"n.cs","side":"mid","from":1,"to":2}]}]`,
		`[{"name":"x","lines":[{"path":"missing.cs","from":1,"to":2}]}]`,
		`[{"name":"x","lines":[{"path":"n.cs","from":5,"to":2}]}]`,
	}
	for _, b := range bad {
		if err := s.SetSections([]byte(b)); err == nil {
			t.Errorf("should be rejected: %s", b)
		}
	}

	secs := `[
		{"name":"A","lines":[{"path":"n.cs","from":1,"to":10}]},
		{"name":"B","lines":[{"path":"n.cs","side":"new","from":11,"to":20}]},
		{"name":"Rest","files":["a.cs","b.cs","c.txt"]}
	]`
	if err := s.SetSections([]byte(secs)); err != nil {
		t.Fatal(err)
	}
	a := s.Section("A")
	if len(a.Hunks) != 0 || len(a.Ranges) != 1 || a.Ranges[0] != (HunkRange{nh, 0, 9}) || len(a.Files) != 1 {
		t.Errorf("section A: %+v", a)
	}
	if got := s.HunkSections(nh); len(got) != 2 {
		t.Errorf("n.cs hunk should belong to A and B, got %v", got)
	}
	if un, _ := s.Unassigned(); len(un) != 0 {
		t.Errorf("no hunk is wholly unassigned, got %v", un)
	}
	if rest := s.UnassignedRanges(); len(rest) != 1 || rest[0] != (HunkRange{nh, 20, 29}) {
		t.Errorf("lines 21-30 should be left unassigned, got %+v", rest)
	}

	// 範囲を変えると、そのセクションの確認済みは外れる。変えていないセクションは残る。
	for _, k := range []string{"A", "B"} {
		if err := s.SetMark("section", k, true, store.AuthorWorker); err != nil {
			t.Fatal(err)
		}
	}
	secs2 := strings.Replace(secs, `"from":1,"to":10`, `"from":1,"to":12`, 1)
	if err := s.SetSections([]byte(secs2)); err != nil {
		t.Fatal(err)
	}
	if s.SectionMarked("A") {
		t.Error("changing a section's range should drop its mark")
	}
	if !s.SectionMarked("B") {
		t.Error("an unchanged section should keep its mark")
	}

	// 同じセクションで丸ごとと範囲が重なれば、丸ごとだけになる。範囲は変更の行に合わせて取る。
	both := `[{"name":"W","files":["n.cs"],"lines":[{"path":"n.cs","from":3,"to":4}]},
		{"name":"M","lines":[{"path":"a.cs","from":3,"to":3}]},
		{"name":"Rest","files":["b.cs","c.txt"]}]`
	if err := s.SetSections([]byte(both)); err != nil {
		t.Fatal(err)
	}
	if w := s.Section("W"); len(w.Hunks) != 1 || len(w.Ranges) != 0 {
		t.Errorf("whole should win over a range in the same section: %+v", w)
	}
	m := s.Section("M")
	if len(m.Ranges) != 1 {
		t.Fatalf("section M: %+v", m)
	}
	ah := s.Hunk(m.Ranges[0].Hunk)
	for i := m.Ranges[0].Start; i <= m.Ranges[0].End; i++ {
		if l := ah.Lines[i]; l.New != 3 && l.Kind != '-' {
			t.Errorf("range on a.cs line 3 picked line %+v", l)
		}
	}
	if rest := s.UnassignedRanges(); len(rest) == 0 {
		t.Error("the rest of a.cs's hunk should be left unassigned")
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"Assets/App/A.cs", nil, true},
		{"Assets/App/A.cs", []string{"Assets/App"}, true},
		{"Assets/App/A.cs", []string{"Assets/App/"}, true},
		{"Assets/Apple/A.cs", []string{"Assets/App"}, false},
		{"Assets/App/A.cs", []string{"*.cs"}, true},
		{"Assets/App/A.cs.meta", []string{"*.cs"}, false},
		{"Assets/App/A.cs", []string{"Assets/*/A.cs"}, true},
		{"Assets/App/A.cs", []string{"Docs", "*.cs"}, true},
		{"Assets/App/A.cs", []string{"Assets/App/A.cs"}, true},
		{"Assets/App/A.cs", []string{"A.cs"}, true},
		{"Assets/App/AB.cs", []string{"A.cs"}, false},
	}
	for _, c := range cases {
		if got := MatchPath(c.path, c.patterns); got != c.want {
			t.Errorf("MatchPath(%q, %v) = %v", c.path, c.patterns, got)
		}
	}
}

func TestLinesAnchorSurvivesCommit(t *testing.T) {
	dir := setupRepo(t)
	write(t, dir, "n.cs", lines("n", 30))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "new file")
	if _, err := Init(dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	s, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	byLines, err := s.ParseAnchor("lines:n.cs:new:5-6")
	if err != nil {
		t.Fatal(err)
	}
	if byLines.Type != store.AnchorLines || len(byLines.Text) != 2 || byLines.Text[0] != "+n----" {
		t.Fatalf("lines anchor: %+v", byLines)
	}
	nh := s.File("n.cs").Hunks[0].ID
	byIndex, err := s.ParseAnchor("line:" + nh + ":new:4-5")
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.NewThread("question", byLines, "by lines", store.AuthorWorker)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.NewThread("question", byIndex, "by index", store.AuthorWorker)
	if err != nil {
		t.Fatal(err)
	}
	if hunk, end, ok := s.Place(t1.Anchor); !ok || hunk != nh || end != 5 {
		t.Errorf("place before commit: %s %d %v", hunk, end, ok)
	}

	// 同じ新しいファイルの別の行を直す。hunk の ID は変わる。
	write(t, dir, "n.cs", strings.Replace(lines("n", 30), "n"+strings.Repeat("-", 20)+"\n", "changed\n", 1))
	git(t, dir, "commit", "-q", "-am", "edit line 21")
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if s.File("n.cs").Hunks[0].ID == nh {
		t.Fatal("the hunk ID should change after the edit")
	}
	if _, ok := s.Describe(t1.Anchor); !ok {
		t.Error("a lines anchor should still resolve after the commit")
	}
	if hunk, end, ok := s.Place(t1.Anchor); !ok || hunk != s.File("n.cs").Hunks[0].ID || end != 5 {
		t.Errorf("place after commit: %s %d %v", hunk, end, ok)
	}
	if _, ok := s.Describe(t2.Anchor); ok {
		t.Error("a hunk-index anchor is expected to be orphaned (that is the old behaviour)")
	}
	if _, err := s.ParseAnchor("lines:n.cs:new:100-101"); err == nil {
		t.Error("lines outside the diff should be rejected")
	}
	if _, err := s.ParseAnchor("lines:n.cs:5-6"); err == nil {
		t.Error("a lines anchor without a side should be rejected")
	}
}

func TestDeleteAndAgentClose(t *testing.T) {
	dir := setup(t)
	s, err := Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	review := store.Anchor{Type: store.AnchorReview}
	a, _ := s.NewThread("question", review, "a", store.AuthorWorker)
	b, _ := s.NewThread("question", review, "b", store.AuthorWorker)
	c, _ := s.NewThread("question", review, "c", store.AuthorWorker)

	if _, err := s.AgentClose(a.ID); err == nil {
		t.Error("the agent must not close a thread the worker did not allow")
	}
	if _, err := s.SetAgentMayClose(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if th, err := s.AgentClose(a.ID); err != nil || th.State != store.StateClosed {
		t.Errorf("agent close after permission: %+v %v", th, err)
	}

	if _, err := s.DeleteThreads(nil, nil, false); err == nil {
		t.Error("deleting needs a selection")
	}
	if _, err := s.DeleteThreads(nil, []string{"bogus"}, false); err == nil {
		t.Error("an unknown state should be rejected")
	}
	if n, err := s.DeleteThreads(nil, []string{store.StateClosed}, false); err != nil || n != 1 {
		t.Errorf("delete closed: %d %v", n, err)
	}
	if n, err := s.DeleteThreads([]string{b.ID}, nil, false); err != nil || n != 1 || s.Threads[b.ID] != nil {
		t.Errorf("delete by id: %d %v", n, err)
	}
	again, err := Load(dir, s.Review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Threads) != 1 || again.Threads[c.ID] == nil {
		t.Errorf("deletions should be saved, left: %v", again.Threads)
	}
	if n, err := s.DeleteThreads(nil, nil, true); err != nil || n != 1 || len(s.Threads) != 0 {
		t.Errorf("delete all: %d %v", n, err)
	}
}
