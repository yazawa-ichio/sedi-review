package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yazawa-ichio/sedi-review/internal/gitx"
)

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`plain/path.txt`:                   `plain/path.txt`,
		`"a/\346\227\245\346\234\254.txt"`: "a/日本.txt",
		`"a/tab\there"`:                    "a/tab\there",
		`"a/q\"uote\\"`:                    `a/q"uote\`,
	}
	for in, want := range cases {
		got, err := unquote(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestSplitPathPair(t *testing.T) {
	cases := [][3]string{
		{`a/x y.txt b/x y.txt`, `a/x y.txt`, `b/x y.txt`},
		{`"a/\346.txt" "b/\346.txt"`, `"a/\346.txt"`, `"b/\346.txt"`},
	}
	for _, c := range cases {
		a, b, err := splitPathPair(c[0])
		if err != nil || a != c[1] || b != c[2] {
			t.Errorf("%s: got %q %q %v", c[0], a, b, err)
		}
	}
}

func TestIDIgnoresLineNumbers(t *testing.T) {
	a := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@ ctx1\n x\n-y\n+z\n"
	b := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -40,2 +41,2 @@ other\n x\n-y\n+z\n"
	fa, err := Parse([]byte(a), Prefixes{"a/", "b/"})
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Parse([]byte(b), Prefixes{"a/", "b/"})
	if err != nil {
		t.Fatal(err)
	}
	if fa[0].Hunks[0].ID != fb[0].Hunks[0].ID {
		t.Fatalf("line numbers changed the ID")
	}
}

func TestDuplicateHunksGetDistinctIDs(t *testing.T) {
	src := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-y\n+z\n@@ -9 +9 @@\n-y\n+z\n"
	fs, err := Parse([]byte(src), Prefixes{"a/", "b/"})
	if err != nil {
		t.Fatal(err)
	}
	h := fs[0].Hunks
	if len(h) != 2 || h[0].ID == h[1].ID {
		t.Fatalf("want 2 distinct ids, got %+v", h)
	}
}

func TestPathChangesID(t *testing.T) {
	a := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-y\n+z\n"
	b := "diff --git a/g b/g\n--- a/g\n+++ b/g\n@@ -1 +1 @@\n-y\n+z\n"
	fa, _ := Parse([]byte(a), Prefixes{"a/", "b/"})
	fb, _ := Parse([]byte(b), Prefixes{"a/", "b/"})
	if fa[0].Hunks[0].ID == fb[0].Hunks[0].ID {
		t.Fatal("path should be part of the ID")
	}
}

func TestRejectsColorAndExternal(t *testing.T) {
	if _, err := Parse([]byte("\x1b[1mdiff --git a/f b/f\n"), Prefixes{}); err == nil {
		t.Error("color output should fail")
	}
	if _, err := Parse([]byte("something else\n"), Prefixes{}); err == nil {
		t.Error("non-standard output should fail")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func numbered(n int, prefix string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(prefix)
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	return b.String()
}

func TestRealGitOutput(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "core.autocrlf", "false")
	lines := numbered(40, "line")
	write(t, dir, "keep.txt", lines)
	write(t, dir, "move_me.txt", numbered(30, "mv"))
	write(t, dir, "pure.txt", "pure\n")
	write(t, dir, "mode.sh", "echo\n")
	write(t, dir, "del.txt", "bye\n")
	write(t, dir, "日本語.txt", "あ\nい\n")
	write(t, dir, "nonl.txt", "a\nb")
	write(t, dir, "img.png", "\x89PNG\r\n\x1a\n\x00\x00old")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "1")

	changed := strings.Replace(lines, "line\n", "LINE\n", 1)
	changed = strings.Replace(changed, "linexxxxx\n", "edited\n", 1)
	write(t, dir, "keep.txt", changed+"tail\n")
	git(t, dir, "mv", "move_me.txt", "moved.txt")
	write(t, dir, "moved.txt", strings.Replace(numbered(30, "mv"), "mvxx\n", "MOVED\n", 1))
	git(t, dir, "mv", "pure.txt", "pure2.txt")
	git(t, dir, "update-index", "--chmod=+x", "mode.sh")
	git(t, dir, "rm", "-q", "del.txt")
	write(t, dir, "日本語.txt", "あ\nう\n")
	write(t, dir, "nonl.txt", "a\nc")
	write(t, dir, "img.png", "\x89PNG\r\n\x1a\n\x00\x00new")
	write(t, dir, "new.txt", "hello\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "2")

	out, err := gitx.Diff(dir, "HEAD~1", "HEAD", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Parse(out, Prefixes{"a/", "b/"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byPath := map[string]*File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	for _, skipped := range []string{"pure2.txt", "pure.txt", "mode.sh"} {
		if _, ok := byPath[skipped]; ok {
			t.Errorf("%s has no content diff and should be skipped", skipped)
		}
	}
	if f := byPath["keep.txt"]; f == nil || len(f.Hunks) < 2 {
		t.Errorf("keep.txt: want several hunks, got %+v", f)
	}
	if f := byPath["moved.txt"]; f == nil || f.Status != "renamed" || f.OldPath != "move_me.txt" || len(f.Hunks) == 0 {
		t.Errorf("moved.txt: got %+v", f)
	}
	if f := byPath["del.txt"]; f == nil || f.Status != "deleted" {
		t.Errorf("del.txt: got %+v", f)
	}
	if f := byPath["new.txt"]; f == nil || f.Status != "added" {
		t.Errorf("new.txt: got %+v", f)
	}
	if f := byPath["日本語.txt"]; f == nil || len(f.Hunks) != 1 {
		t.Errorf("日本語.txt: got %+v (paths: %v)", f, keys(byPath))
	}
	if f := byPath["img.png"]; f == nil || !f.Binary || f.OldBlob == "" || f.NewBlob == "" {
		t.Errorf("img.png: got %+v", f)
	}
	if f := byPath["nonl.txt"]; f == nil {
		t.Error("nonl.txt missing")
	} else {
		marks := 0
		for _, l := range f.Hunks[0].Lines {
			if l.Kind == '\\' {
				marks++
			}
		}
		if marks != 2 {
			t.Errorf("nonl.txt: want 2 no-newline markers, got %d", marks)
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		for _, h := range f.Hunks {
			if seen[h.ID] {
				t.Errorf("duplicate id %s", h.ID)
			}
			seen[h.ID] = true
		}
	}
}

func keys(m map[string]*File) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
