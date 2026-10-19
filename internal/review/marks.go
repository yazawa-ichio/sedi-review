package review

import (
	"fmt"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/store"
)

func (s *State) FileMarked(path string) bool {
	m, ok := s.Marks.Files[path]
	f := s.files[path]
	return ok && f != nil && f.Fingerprint == m.Fingerprint
}

func (s *State) SectionMarked(key string) bool {
	m, ok := s.Marks.Sections[key]
	info := s.index.byKey[key]
	return ok && info != nil && info.Fingerprint == m.Fingerprint
}

// SetMark は確認済みを付け外しする。エージェントは外すことしかできない。
func (s *State) SetMark(kind, key string, on bool, author string) error {
	if on && author != store.AuthorWorker {
		return fmt.Errorf("確認済みを付けられるのは作業者だけです")
	}
	switch kind {
	case "file":
		f := s.files[key]
		if f == nil {
			return fmt.Errorf("ファイル %s は差分にありません", key)
		}
		if on {
			s.Marks.Files[key] = store.Mark{Fingerprint: f.Fingerprint, At: time.Now()}
		} else {
			delete(s.Marks.Files, key)
		}
	case "section":
		info := s.index.byKey[key]
		if info == nil {
			return fmt.Errorf("セクション %s がありません", key)
		}
		if on {
			s.Marks.Sections[key] = store.Mark{Fingerprint: info.Fingerprint, At: time.Now()}
		} else {
			delete(s.Marks.Sections, key)
		}
	default:
		return fmt.Errorf("確認済みを付けられるのはファイルとセクションだけです: %s", kind)
	}
	return s.saveMarks()
}
