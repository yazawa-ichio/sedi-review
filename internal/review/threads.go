package review

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/store"
)

// ParseAnchor は CLI の書き方（review / section:鍵 / file:パス / hunk:ID / line:ID:old|new:始め-終わり）を読む。
func (s *State) ParseAnchor(spec string) (store.Anchor, error) {
	kind, rest, _ := strings.Cut(spec, ":")
	a := store.Anchor{Type: kind}
	switch kind {
	case store.AnchorReview:
	case store.AnchorSection:
		a.Section = rest
	case store.AnchorFile:
		a.Path = rest
	case store.AnchorHunk:
		a.Hunk = rest
	case store.AnchorLine:
		parts := strings.Split(rest, ":")
		if len(parts) != 3 {
			return a, fmt.Errorf("行の付け先は line:ID:old|new:始め-終わり で書きます: %s", spec)
		}
		a.Hunk, a.Side = parts[0], parts[1]
		from, to, ok := strings.Cut(parts[2], "-")
		if !ok {
			to = from
		}
		var err error
		if a.Start, err = strconv.Atoi(from); err != nil {
			return a, fmt.Errorf("行の位置が数字ではありません: %s", spec)
		}
		if a.End, err = strconv.Atoi(to); err != nil {
			return a, fmt.Errorf("行の位置が数字ではありません: %s", spec)
		}
	default:
		return a, fmt.Errorf("付け先の種類が不正です: %s", spec)
	}
	return s.CompleteAnchor(a)
}

// CompleteAnchor は付け先を確かめ、パスと行の写しを埋める。
func (s *State) CompleteAnchor(a store.Anchor) (store.Anchor, error) {
	switch a.Type {
	case store.AnchorReview:
		return store.Anchor{Type: a.Type}, nil
	case store.AnchorSection:
		if s.index.byKey[a.Section] == nil {
			return a, fmt.Errorf("セクション %s がありません", a.Section)
		}
	case store.AnchorFile:
		if s.files[a.Path] == nil {
			return a, fmt.Errorf("ファイル %s は差分にありません", a.Path)
		}
	case store.AnchorHunk, store.AnchorLine:
		h := s.hunks[a.Hunk]
		if h == nil {
			return a, fmt.Errorf("hunk %s は差分にありません", a.Hunk)
		}
		a.Path = s.hunkFile[a.Hunk].Path
		if a.Type == store.AnchorLine {
			if a.Side != "old" && a.Side != "new" {
				return a, fmt.Errorf("行の側は old か new です: %s", a.Side)
			}
			if a.Start < 0 || a.End < a.Start || a.End >= len(h.Lines) {
				return a, fmt.Errorf("行の位置が hunk の外です: %d-%d", a.Start, a.End)
			}
			a.Text = nil
			for _, l := range h.Lines[a.Start : a.End+1] {
				a.Text = append(a.Text, string(rune(l.Kind))+l.Text)
			}
		}
	default:
		return a, fmt.Errorf("付け先の種類が不正です: %s", a.Type)
	}
	return a, nil
}

// Describe は付け先を人が読める形にする。見つからなければ ok=false。
func (s *State) Describe(a store.Anchor) (string, bool) {
	switch a.Type {
	case store.AnchorReview:
		return "全体", true
	case store.AnchorSection:
		return "セクション「" + a.Section + "」", s.index.byKey[a.Section] != nil
	case store.AnchorFile:
		return "ファイル " + a.Path, s.files[a.Path] != nil
	case store.AnchorHunk:
		h := s.hunks[a.Hunk]
		if h == nil {
			return fmt.Sprintf("hunk %s（%s）", a.Hunk, a.Path), false
		}
		return fmt.Sprintf("hunk h%d %s（%s @@ -%d,%d +%d,%d @@）", h.Num, a.Hunk, a.Path, h.OldStart, h.OldCount, h.NewStart, h.NewCount), true
	case store.AnchorLine:
		h := s.hunks[a.Hunk]
		side := "変更後"
		if a.Side == "old" {
			side = "変更前"
		}
		if h == nil || a.End >= len(h.Lines) {
			return fmt.Sprintf("行 %s %s（hunk %s の %d〜%d、付け先なし）", a.Path, side, a.Hunk, a.Start, a.End), false
		}
		first, last := 0, 0
		for _, l := range h.Lines[a.Start : a.End+1] {
			n := l.New
			if a.Side == "old" {
				n = l.Old
			}
			if n == 0 {
				continue
			}
			if first == 0 {
				first = n
			}
			last = n
		}
		return fmt.Sprintf("行 %s %s %d〜%d 行（hunk h%d %s）", a.Path, side, first, last, h.Num, a.Hunk), true
	}
	return a.Type, false
}

func (s *State) ThreadList() []*store.Thread {
	var out []*store.Thread
	for _, t := range s.Threads {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *State) thread(id string) (*store.Thread, error) {
	t := s.Threads[id]
	if t == nil {
		return nil, fmt.Errorf("スレッド %s がありません", id)
	}
	return t, nil
}

func (s *State) NewThread(kind string, a store.Anchor, body, author string) (*store.Thread, error) {
	if _, ok := store.Kinds[kind]; !ok {
		return nil, fmt.Errorf("種類が不正です: %s（instruction / question / reconsider / other）", kind)
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("本文が空です")
	}
	a, err := s.CompleteAnchor(a)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	t := &store.Thread{ID: store.NewID(), Kind: kind, Anchor: a, CreatedAt: now}
	s.append(t, body, author, now)
	s.Threads[t.ID] = t
	return t, store.SaveThread(s.Dir, t)
}

func (s *State) append(t *store.Thread, body, author string, now time.Time) {
	m := store.Message{ID: store.NewID(), Author: author, Body: body, At: now}
	if author == store.AuthorWorker {
		t.State = store.StateUnsent
	} else {
		m.Sent = true
		t.State = store.StateReplied
		t.Pending = false
	}
	t.Messages = append(t.Messages, m)
	t.UpdatedAt = now
}

func (s *State) AddMessage(id, body, author string) (*store.Thread, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("本文が空です")
	}
	s.append(t, body, author, time.Now())
	return t, store.SaveThread(s.Dir, t)
}

// EditMessage は作業者のまだ送っていないメッセージだけを書き直す。
func (s *State) EditMessage(id, msgID, body string) (*store.Thread, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	for i := range t.Messages {
		m := &t.Messages[i]
		if m.ID != msgID {
			continue
		}
		if m.Author != store.AuthorWorker || m.Sent {
			return nil, fmt.Errorf("送ったメッセージは編集できません")
		}
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("本文が空です")
		}
		m.Body = body
		t.UpdatedAt = time.Now()
		return t, store.SaveThread(s.Dir, t)
	}
	return nil, fmt.Errorf("メッセージ %s がありません", msgID)
}

// DeleteMessage は作業者のまだ送っていないメッセージを取り消す。空になったスレッドは消す。
func (s *State) DeleteMessage(id, msgID string) (*store.Thread, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	for i, m := range t.Messages {
		if m.ID != msgID {
			continue
		}
		if m.Author != store.AuthorWorker || m.Sent {
			return nil, fmt.Errorf("送ったメッセージは取り消せません")
		}
		t.Messages = append(t.Messages[:i], t.Messages[i+1:]...)
		if len(t.Messages) == 0 {
			delete(s.Threads, id)
			return nil, store.DeleteThread(s.Dir, id)
		}
		t.State = stateAfter(t)
		t.UpdatedAt = time.Now()
		return t, store.SaveThread(s.Dir, t)
	}
	return nil, fmt.Errorf("メッセージ %s がありません", msgID)
}

func stateAfter(t *store.Thread) string {
	for _, m := range t.Messages {
		if m.Author == store.AuthorWorker && !m.Sent {
			return store.StateUnsent
		}
	}
	if last := t.Messages[len(t.Messages)-1]; last.Author == store.AuthorAgent {
		return store.StateReplied
	}
	return store.StateSent
}

func (s *State) Close(id string) (*store.Thread, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	t.State = store.StateClosed
	t.Pending = false
	t.UpdatedAt = time.Now()
	return t, store.SaveThread(s.Dir, t)
}

func (s *State) Reattach(id string, a store.Anchor) (*store.Thread, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	if t.Anchor, err = s.CompleteAnchor(a); err != nil {
		return nil, err
	}
	t.UpdatedAt = time.Now()
	return t, store.SaveThread(s.Dir, t)
}

// SetPending は待ち受けに渡す予定にする。
func (s *State) SetPending(id string) (*store.Thread, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	if t.State == store.StateClosed {
		return nil, fmt.Errorf("閉じたスレッドは送れません")
	}
	t.Pending = true
	return t, store.SaveThread(s.Dir, t)
}

func (s *State) PendingThreads() []*store.Thread {
	var out []*store.Thread
	for _, t := range s.ThreadList() {
		if t.Pending {
			out = append(out, t)
		}
	}
	return out
}

// MarkSent は渡したスレッドの作業者のメッセージを送付済みにする。
func (s *State) MarkSent(threads []*store.Thread) error {
	for _, t := range threads {
		changed := t.Pending
		t.Pending = false
		for i := range t.Messages {
			if t.Messages[i].Author == store.AuthorWorker && !t.Messages[i].Sent {
				t.Messages[i].Sent = true
				changed = true
			}
		}
		if t.State == store.StateUnsent {
			t.State = store.StateSent
			changed = true
		}
		if changed {
			if err := store.SaveThread(s.Dir, t); err != nil {
				return err
			}
		}
	}
	return nil
}
