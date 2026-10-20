// Package skill は、実行ファイルに埋め込んだエージェント向けの skill を、各エージェントの置き場所に写す。
package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

var Agents = []string{"claude", "codex"}

const (
	ScopeUser    = "user"
	ScopeProject = "project"
)

// SkillsDir は skill を並べるフォルダ（その下に skill ごとのフォルダを置く）を返す。
// user なら home の下、project なら repo の下。
func SkillsDir(agent, scope, home, repo string) (string, error) {
	var base string
	switch scope {
	case ScopeUser:
		switch agent {
		case "claude":
			base = filepath.Join(home, ".claude")
			if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
				base = v
			}
		case "codex":
			base = filepath.Join(home, ".agents")
		}
	case ScopeProject:
		if repo == "" {
			return "", fmt.Errorf("project に入れるにはリポジトリが要ります")
		}
		switch agent {
		case "claude":
			base = filepath.Join(repo, ".claude")
		case "codex":
			base = filepath.Join(repo, ".agents")
		}
	default:
		return "", fmt.Errorf("scope は user か project です: %q", scope)
	}
	if base == "" {
		return "", fmt.Errorf("agent は %s のどれかです: %q", strings.Join(Agents, " / "), agent)
	}
	return filepath.Join(base, "skills"), nil
}

// Names は、埋め込んだ skill の名前（skills の下のフォルダ名）を返す。
// Agent Skills の決まりで、フォルダ名は SKILL.md の name と同じにする。
func Names(root fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := fs.Stat(root, path.Join(e.Name(), "SKILL.md")); err == nil {
				names = append(names, e.Name())
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

const (
	NotInstalled = "入っていない"
	UpToDate     = "この版と同じ"
	Differs      = "この版と違う（古い版か、手で直したもの）"
)

// Status は dest の中身が src と同じかを返す。src に無いファイルは見ない。
func Status(src fs.FS, dest string) (string, error) {
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
		return NotInstalled, nil
	}
	files, err := list(src)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		want, err := fs.ReadFile(src, f)
		if err != nil {
			return "", err
		}
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(f)))
		if err != nil || !bytes.Equal(want, got) {
			return Differs, nil
		}
	}
	return UpToDate, nil
}

// Install は src を dest に写す。違うものが入っていれば、force が無い限り写さない。
func Install(src fs.FS, dest string, force bool) (string, error) {
	st, err := Status(src, dest)
	if err != nil {
		return "", err
	}
	switch {
	case st == UpToDate:
		return "既に同じ版が入っています: " + dest, nil
	case st == Differs && !force:
		return "", fmt.Errorf("%s には違う中身の skill が入っています。置き換えるときは --force を付けてください", dest)
	}
	files, err := list(src)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		data, err := fs.ReadFile(src, f)
		if err != nil {
			return "", err
		}
		to := filepath.Join(dest, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", err
		}
		if err := writeFile(to, data); err != nil {
			return "", err
		}
	}
	if st == Differs {
		return "置き換えました: " + dest, nil
	}
	return "入れました: " + dest, nil
}

// Uninstall は src と同じ名前のファイルを dest から消し、空になったフォルダを消す。
// 手で直したものは、force が無い限り消さない。
func Uninstall(src fs.FS, dest string, force bool) (string, error) {
	st, err := Status(src, dest)
	if err != nil {
		return "", err
	}
	switch {
	case st == NotInstalled:
		return "入っていません: " + dest, nil
	case st == Differs && !force:
		return "", fmt.Errorf("%s の skill はこの版と中身が違います。消すときは --force を付けてください", dest)
	}
	files, err := list(src)
	if err != nil {
		return "", err
	}
	dirs := map[string]bool{dest: true}
	for _, f := range files {
		p := filepath.Join(dest, filepath.FromSlash(f))
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		for d := filepath.Dir(p); d != dest && strings.HasPrefix(d, dest); d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	// 深いフォルダから、空なら消す。利用者が足したファイルがあれば残る。
	var ordered []string
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, d := range ordered {
		os.Remove(d)
	}
	if _, err := os.Stat(dest); err == nil {
		return "skill のファイルを消しました。フォルダに他のファイルが残っています: " + dest, nil
	}
	return "消しました: " + dest, nil
}

func list(src fs.FS) ([]string, error) {
	var files []string
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path.Clean(p))
		}
		return nil
	})
	return files, err
}

func writeFile(to string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(to), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, to); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
