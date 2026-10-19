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

type SectionInfo struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Intent      string   `json:"intent,omitempty"`
	Checks      []string `json:"checks,omitempty"`
	Depth       int      `json:"depth"`
	Hunks       []string `json:"hunks"`
	Files       []string `json:"files"` // 属する hunk を持つファイルと、丸ごと入るバイナリ
	Fingerprint string   `json:"fingerprint"`
	Children    []string `json:"children,omitempty"`
}

type sectionIndex struct {
	list        []*SectionInfo
	byKey       map[string]*SectionInfo
	unassigned  []string // hunk の ID
	unassignedB []string // バイナリのパス
	hunkKeys    map[string][]string
}

func (s *State) buildIndex(sections []*store.Section) *sectionIndex {
	idx := &sectionIndex{byKey: map[string]*SectionInfo{}, hunkKeys: map[string][]string{}}
	assignedFiles := map[string]bool{}
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
				prints = append(prints, "hunk:"+id)
			}
			sort.Slice(info.Hunks, func(i, j int) bool { return s.hunks[info.Hunks[i]].Num < s.hunks[info.Hunks[j]].Num })
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
			if len(idx.hunkKeys[h.ID]) == 0 {
				idx.unassigned = append(idx.unassigned, h.ID)
			}
		}
	}
	return idx
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
