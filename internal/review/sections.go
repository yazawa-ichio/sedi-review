package review

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yazawa-ichio/sedi-review/internal/store"
)

const keySep = " / "

// HunkRange は hunk の中の行の範囲（hunk の行の 0 から数えた位置。両端を含む）。
type HunkRange struct {
	Hunk  string `json:"hunk"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type SectionInfo struct {
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Intent      string      `json:"intent,omitempty"`
	Checks      []string    `json:"checks,omitempty"`
	Depth       int         `json:"depth"`
	Hunks       []string    `json:"hunks"`  // 丸ごと入る hunk
	Ranges      []HunkRange `json:"ranges"` // 一部だけ入る hunk の範囲
	Files       []string    `json:"files"`  // 属する hunk を持つファイルと、丸ごと入るバイナリ
	Fingerprint string      `json:"fingerprint"`
	Children    []string    `json:"children,omitempty"`
}

type sectionIndex struct {
	list        []*SectionInfo
	byKey       map[string]*SectionInfo
	unassigned  []string    // どのセクションにも入らない hunk の ID
	unassignedR []HunkRange // 一部だけがセクションに入った hunk の、残りの範囲
	unassignedB []string    // バイナリのパス
	hunkKeys    map[string][]string
}

func (s *State) buildIndex(sections []*store.Section) *sectionIndex {
	idx := &sectionIndex{byKey: map[string]*SectionInfo{}, hunkKeys: map[string][]string{}}
	assignedFiles := map[string]bool{}
	whole := map[string]bool{}
	covered := map[string][][2]int{}
	var walk func(list []*store.Section, parent string, depth int) []string
	walk = func(list []*store.Section, parent string, depth int) []string {
		var keys []string
		for _, sec := range list {
			key := sec.Name
			if parent != "" {
				key = parent + keySep + sec.Name
			}
			info := &SectionInfo{Key: key, Name: sec.Name, Intent: sec.Intent, Checks: sec.Checks, Depth: depth}
			idx.list = append(idx.list, info)
			idx.byKey[key] = info
			hunkSet := map[string]bool{}
			fileSet := map[string]bool{}
			var prints []string
			for _, path := range sec.Files {
				f := s.files[path]
				if f == nil {
					continue
				}
				fileSet[path] = true
				assignedFiles[path] = true
				if f.Binary {
					prints = append(prints, "file:"+f.Fingerprint)
				}
				for _, h := range f.Hunks {
					hunkSet[h.ID] = true
				}
			}
			for _, id := range sec.Hunks {
				if f := s.hunkFile[id]; f != nil {
					hunkSet[id] = true
					fileSet[f.Path] = true
				}
			}
			for id := range hunkSet {
				info.Hunks = append(info.Hunks, id)
				idx.hunkKeys[id] = append(idx.hunkKeys[id], key)
				whole[id] = true
				prints = append(prints, "hunk:"+id)
			}
			sort.Slice(info.Hunks, func(i, j int) bool { return s.hunks[info.Hunks[i]].Num < s.hunks[info.Hunks[j]].Num })

			parts := map[string][][2]int{}
			for _, lr := range sec.Lines {
				ranges, _ := s.resolveLines(lr)
				for _, r := range ranges {
					if hunkSet[r.Hunk] {
						continue
					}
					parts[r.Hunk] = append(parts[r.Hunk], [2]int{r.Start, r.End})
				}
			}
			for id, list := range parts {
				merged := mergeRanges(list)
				fileSet[s.hunkFile[id].Path] = true
				idx.hunkKeys[id] = append(idx.hunkKeys[id], key)
				covered[id] = append(covered[id], merged...)
				for _, r := range merged {
					info.Ranges = append(info.Ranges, HunkRange{Hunk: id, Start: r[0], End: r[1]})
					prints = append(prints, fmt.Sprintf("range:%s:%d-%d", id, r[0], r[1]))
				}
			}
			sort.Slice(info.Ranges, func(i, j int) bool {
				a, b := info.Ranges[i], info.Ranges[j]
				if a.Hunk != b.Hunk {
					return s.hunks[a.Hunk].Num < s.hunks[b.Hunk].Num
				}
				return a.Start < b.Start
			})
			info.Files = s.orderPaths(fileSet)
			sort.Strings(prints)
			info.Fingerprint = hashOf(prints...)
			info.Children = walk(sec.Children, key, depth+1)
			keys = append(keys, key)
		}
		return keys
	}
	walk(sections, "", 0)
	for _, f := range s.Files {
		if f.Binary {
			if !assignedFiles[f.Path] {
				idx.unassignedB = append(idx.unassignedB, f.Path)
			}
			continue
		}
		for _, h := range f.Hunks {
			switch {
			case whole[h.ID]:
			case len(covered[h.ID]) == 0:
				idx.unassigned = append(idx.unassigned, h.ID)
			default:
				for _, r := range s.uncovered(h.ID, covered[h.ID]) {
					idx.unassignedR = append(idx.unassignedR, HunkRange{Hunk: h.ID, Start: r[0], End: r[1]})
				}
			}
		}
	}
	return idx
}

// resolveLines はファイルの行の範囲を、hunk ごとの範囲に置き換える。
// 範囲の行を含む hunk の中で、最初と最後の該当する行の間をまとめて取る（間の削除の行も入る）。
func (s *State) resolveLines(lr store.LineRange) ([]HunkRange, error) {
	f := s.files[lr.Path]
	if f == nil {
		return nil, fmt.Errorf("ファイル %s は差分にありません", lr.Path)
	}
	side := lr.Side
	if side == "" {
		side = "new"
	}
	if side != "new" && side != "old" {
		return nil, fmt.Errorf("%s: side は new か old です: %q", lr.Path, lr.Side)
	}
	if lr.From < 1 || lr.To < lr.From {
		return nil, fmt.Errorf("%s: 行の範囲が不正です: %d-%d", lr.Path, lr.From, lr.To)
	}
	var out []HunkRange
	for _, h := range f.Hunks {
		first, last := -1, -1
		for i, l := range h.Lines {
			n := l.New
			if side == "old" {
				n = l.Old
			}
			if n >= lr.From && n <= lr.To {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first >= 0 {
			out = append(out, HunkRange{Hunk: h.ID, Start: first, End: last})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %s の %d〜%d 行は差分の hunk に入っていません", lr.Path, side, lr.From, lr.To)
	}
	return out, nil
}

func mergeRanges(list [][2]int) [][2]int {
	sort.Slice(list, func(i, j int) bool { return list[i][0] < list[j][0] })
	var out [][2]int
	for _, r := range list {
		if n := len(out); n > 0 && r[0] <= out[n-1][1]+1 {
			if r[1] > out[n-1][1] {
				out[n-1][1] = r[1]
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// uncovered は hunk のうち、どの範囲にも入らない部分を返す。変更の行（+ と -）を含む部分だけを返す。
func (s *State) uncovered(id string, covered [][2]int) [][2]int {
	h := s.hunks[id]
	merged := mergeRanges(append([][2]int(nil), covered...))
	var out [][2]int
	add := func(a, b int) {
		for i := a; i <= b; i++ {
			if k := h.Lines[i].Kind; k == '+' || k == '-' {
				out = append(out, [2]int{a, b})
				return
			}
		}
	}
	next := 0
	for _, r := range merged {
		if r[0] > next {
			add(next, r[0]-1)
		}
		next = r[1] + 1
	}
	if next <= len(h.Lines)-1 {
		add(next, len(h.Lines)-1)
	}
	return out
}

func (s *State) orderPaths(set map[string]bool) []string {
	var out []string
	for _, f := range s.Files {
		if set[f.Path] {
			out = append(out, f.Path)
		}
	}
	return out
}

func (s *State) Sections() []*SectionInfo        { return s.index.list }
func (s *State) Section(key string) *SectionInfo { return s.index.byKey[key] }
func (s *State) RawSections() []*store.Section   { return s.sections }
func (s *State) HunkSections(id string) []string { return s.index.hunkKeys[id] }

// Unassigned はどのセクションにも入らない hunk の ID とバイナリのパスを返す。
func (s *State) Unassigned() ([]string, []string) { return s.index.unassigned, s.index.unassignedB }

// UnassignedRanges は、一部だけがセクションに入った hunk の、残りの範囲を返す。
func (s *State) UnassignedRanges() []HunkRange { return s.index.unassignedR }

// SetSections はエージェントの JSON を確かめてから取り込む。
func (s *State) SetSections(data []byte) error {
	var secs []*store.Section
	if err := json.Unmarshal(data, &secs); err != nil {
		return fmt.Errorf("セクションの JSON を読めません: %w", err)
	}
	var errs []string
	var check func(list []*store.Section, parent string)
	check = func(list []*store.Section, parent string) {
		names := map[string]bool{}
		for _, sec := range list {
			where := sec.Name
			if parent != "" {
				where = parent + keySep + sec.Name
			}
			switch {
			case strings.TrimSpace(sec.Name) == "":
				errs = append(errs, fmt.Sprintf("%s: 名前が空です", parent))
			case strings.Contains(sec.Name, keySep):
				errs = append(errs, fmt.Sprintf("%s: 名前に %q は使えません", where, keySep))
			case names[sec.Name]:
				errs = append(errs, fmt.Sprintf("%s: 同じ階層に同じ名前があります", where))
			}
			names[sec.Name] = true
			for _, p := range sec.Files {
				if s.files[p] == nil {
					errs = append(errs, fmt.Sprintf("%s: ファイル %s は差分にありません", where, p))
				}
			}
			for _, id := range sec.Hunks {
				if s.hunks[id] == nil {
					errs = append(errs, fmt.Sprintf("%s: hunk %s は差分にありません", where, id))
				}
			}
			for _, lr := range sec.Lines {
				if _, err := s.resolveLines(lr); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", where, err))
				}
			}
			check(sec.Children, where)
		}
	}
	check(secs, "")
	if len(errs) > 0 {
		return fmt.Errorf("セクションを取り込めません:\n  %s", strings.Join(errs, "\n  "))
	}
	if err := store.WriteJSON(filepath.Join(s.Dir, "sections.json"), secs); err != nil {
		return err
	}
	s.sections = secs
	s.index = s.buildIndex(secs)
	return s.pruneMarks()
}
