// Package review は差分・セクション・確認済み・メッセージをまとめて扱う。
// サーバーと、サーバーが動いていないときの CLI の両方から使う。
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/diff"
	"github.com/yazawa-ichio/sedi-review/internal/gitx"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

type State struct {
	mu sync.Mutex

	Dir     string
	Review  store.Review
	Files   []*diff.File
	Marks   store.Marks
	Threads map[string]*store.Thread

	sections []*store.Section
	index    *sectionIndex
	files    map[string]*diff.File
	hunks    map[string]*diff.Hunk
	hunkFile map[string]*diff.File
}

// Top はリポジトリの最上位を、初期化してあることを確かめてから返す。
func Top(dir string) (string, error) {
	top, err := gitx.TopLevel(dir)
	if err != nil {
		return "", err
	}
	top = filepath.ToSlash(top)
	if _, err := os.Stat(store.Root(top)); err != nil {
		return "", fmt.Errorf("%s は Sedi Review の初期化がまだです。リポジトリで sedi-review init を実行してください", top)
	}
	return top, nil
}

// Create は指定からレビューを作る。同じ指定のレビューがあればそれを開く。
func Create(spec store.Spec) (*State, error) {
	top, err := Top(spec.Repo)
	if err != nil {
		return nil, err
	}
	spec.Repo = top
	id := store.ReviewID(spec)
	dir, err := store.Dir(top, id)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, "review.json")); err == nil {
		return Load(top, id)
	}
	r := store.Review{ID: id, Spec: spec}
	if err := resolve(&r); err != nil {
		return nil, err
	}
	if err := store.WriteJSON(filepath.Join(dir, "review.json"), r); err != nil {
		return nil, err
	}
	return Load(top, id)
}

// Load は repo（リポジトリの中のどこか）に保存してあるレビューを開く。
func Load(repo, id string) (*State, error) {
	top, err := Top(repo)
	if err != nil {
		return nil, err
	}
	dir, err := store.Dir(top, id)
	if err != nil {
		return nil, err
	}
	s := &State{Dir: dir}
	ok, err := store.ReadJSON(filepath.Join(dir, "review.json"), &s.Review)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("レビュー %s が %s にありません", id, top)
	}
	// リポジトリを移しても開けるよう、保存してあるパスではなく今の場所を使う。
	s.Review.Spec.Repo = top
	if _, err := store.ReadJSON(filepath.Join(dir, "sections.json"), &s.sections); err != nil {
		return nil, err
	}
	if _, err := store.ReadJSON(filepath.Join(dir, "marks.json"), &s.Marks); err != nil {
		return nil, err
	}
	if s.Marks.Files == nil {
		s.Marks.Files = map[string]store.Mark{}
	}
	if s.Marks.Sections == nil {
		s.Marks.Sections = map[string]store.Mark{}
	}
	if s.Threads, err = store.LoadThreads(dir); err != nil {
		return nil, err
	}
	if err := s.compute(); err != nil {
		return nil, err
	}
	return s, nil
}

// ListReviews はリポジトリに保存してあるレビューを返す。
func ListReviews(repo string) ([]store.Review, error) {
	top, err := Top(repo)
	if err != nil {
		return nil, err
	}
	dir := store.ReviewsDir(top)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []store.Review
	for _, e := range entries {
		var r store.Review
		if ok, err := store.ReadJSON(filepath.Join(dir, e.Name(), "review.json"), &r); err == nil && ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func resolve(r *store.Review) error {
	base, err := gitx.ResolveCommit(r.Spec.Repo, r.Spec.Base)
	if err != nil {
		return err
	}
	target, err := gitx.ResolveCommit(r.Spec.Repo, r.Spec.Target)
	if err != nil {
		return err
	}
	r.BaseCommit, r.TargetCommit = base, target
	r.UpdatedAt = time.Now()
	return nil
}

func (s *State) Lock()   { s.mu.Lock() }
func (s *State) Unlock() { s.mu.Unlock() }

func (s *State) compute() error {
	cfg, err := LoadConfig(s.Review.Spec.Repo)
	if err != nil {
		return err
	}
	specs, err := cfg.Pathspecs(s.Review.Spec.Rule)
	if err != nil {
		return err
	}
	specs = append(specs, s.Review.Spec.Pathspecs...)
	out, err := gitx.Diff(s.Review.Spec.Repo, s.Review.BaseCommit, s.Review.TargetCommit, s.Review.Spec.ThreeDot, specs)
	if err != nil {
		return err
	}
	files, err := diff.Parse(out, prefixes(s.Review.Spec.Repo))
	if err != nil {
		return err
	}
	s.Files = files
	s.files = map[string]*diff.File{}
	s.hunks = map[string]*diff.Hunk{}
	s.hunkFile = map[string]*diff.File{}
	for _, f := range files {
		s.files[f.Path] = f
		for _, h := range f.Hunks {
			s.hunks[h.ID] = h
			s.hunkFile[h.ID] = f
		}
	}
	s.index = s.buildIndex(s.sections)
	return nil
}

func prefixes(repo string) diff.Prefixes {
	if isTrue(gitx.Config(repo, "diff.noprefix")) {
		return diff.Prefixes{}
	}
	p := diff.Prefixes{Src: "a/", Dst: "b/"}
	if v := gitx.Config(repo, "diff.srcPrefix"); v != "" {
		p.Src = v
	}
	if v := gitx.Config(repo, "diff.dstPrefix"); v != "" {
		p.Dst = v
	}
	return p
}

func isTrue(v string) bool {
	switch strings.ToLower(v) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// Refresh は同じ指定のまま差分を取り直し、中身の変わった確認済みを外す。
func (s *State) Refresh() error {
	r := s.Review
	if err := resolve(&r); err != nil {
		return err
	}
	s.Review = r
	if err := s.compute(); err != nil {
		return err
	}
	if err := store.WriteJSON(filepath.Join(s.Dir, "review.json"), s.Review); err != nil {
		return err
	}
	return s.pruneMarks()
}

func (s *State) pruneMarks() error {
	changed := false
	for path, m := range s.Marks.Files {
		if f := s.files[path]; f == nil || f.Fingerprint != m.Fingerprint {
			delete(s.Marks.Files, path)
			changed = true
		}
	}
	for key, m := range s.Marks.Sections {
		if info := s.index.byKey[key]; info == nil || info.Fingerprint != m.Fingerprint {
			delete(s.Marks.Sections, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveMarks()
}

func (s *State) saveMarks() error {
	return store.WriteJSON(filepath.Join(s.Dir, "marks.json"), s.Marks)
}

func (s *State) File(path string) *diff.File   { return s.files[path] }
func (s *State) Hunk(id string) *diff.Hunk     { return s.hunks[id] }
func (s *State) HunkFile(id string) *diff.File { return s.hunkFile[id] }

func hashOf(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}
