package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/gitx"
	"github.com/yazawa-ichio/sedi-review/internal/review"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

type fileSummary struct {
	Path        string        `json:"path"`
	OldPath     string        `json:"oldPath,omitempty"`
	Status      string        `json:"status"`
	Binary      bool          `json:"binary"`
	Image       bool          `json:"image"`
	Audio       bool          `json:"audio"`
	OldBlob     string        `json:"oldBlob,omitempty"`
	NewBlob     string        `json:"newBlob,omitempty"`
	Marked      bool          `json:"marked"`
	Adds        int           `json:"adds"`
	Dels        int           `json:"dels"`
	Hunks       []hunkSummary `json:"hunks"`
	Fingerprint string        `json:"fingerprint"`
}

type hunkSummary struct {
	ID       string   `json:"id"`
	Num      int      `json:"num"`
	OldStart int      `json:"oldStart"`
	OldCount int      `json:"oldCount"`
	NewStart int      `json:"newStart"`
	NewCount int      `json:"newCount"`
	Context  string   `json:"context,omitempty"`
	Adds     int      `json:"adds"`
	Dels     int      `json:"dels"`
	Sections []string `json:"sections"`
}

type threadView struct {
	*store.Thread
	Where string `json:"where"`
	Found bool   `json:"found"`
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	st := s.st
	st.Lock()
	defer st.Unlock()
	files := make([]fileSummary, 0, len(st.Files))
	for _, f := range st.Files {
		_, media := mediaType(f.Path)
		fs := fileSummary{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Binary: f.Binary, Image: media == "image", Audio: media == "audio",
			OldBlob: realBlob(f.OldBlob), NewBlob: realBlob(f.NewBlob), Marked: st.FileMarked(f.Path), Fingerprint: f.Fingerprint,
			Hunks: []hunkSummary{}}
		for _, h := range f.Hunks {
			fs.Adds += h.Adds
			fs.Dels += h.Dels
			fs.Hunks = append(fs.Hunks, hunkSummary{ID: h.ID, Num: h.Num, OldStart: h.OldStart, OldCount: h.OldCount,
				NewStart: h.NewStart, NewCount: h.NewCount, Context: h.Context, Adds: h.Adds, Dels: h.Dels, Sections: orEmpty(st.HunkSections(h.ID))})
		}
		files = append(files, fs)
	}
	sections := []map[string]any{}
	for _, info := range st.Sections() {
		sections = append(sections, map[string]any{
			"key": info.Key, "name": info.Name, "intent": info.Intent, "checks": orEmpty(info.Checks), "depth": info.Depth,
			"hunks": orEmpty(info.Hunks), "ranges": rangesOrEmpty(info.Ranges), "files": orEmpty(info.Files), "children": orEmpty(info.Children), "marked": st.SectionMarked(info.Key),
		})
	}
	threads := []threadView{}
	for _, t := range st.ThreadList() {
		where, ok := st.Describe(t.Anchor)
		threads = append(threads, threadView{Thread: t, Where: where, Found: ok})
	}
	hunks, binaries := st.Unassigned()
	writeJSON(w, map[string]any{
		"review":     st.Review,
		"files":      files,
		"sections":   sections,
		"unassigned": map[string]any{"hunks": orEmpty(hunks), "ranges": rangesOrEmpty(st.UnassignedRanges()), "files": orEmpty(binaries)},
		"threads":    threads,
		"presence":   s.presence(),
		"kinds":      store.Kinds,
	})
}

func rangesOrEmpty(v []review.HunkRange) []review.HunkRange {
	if v == nil {
		return []review.HunkRange{}
	}
	return v
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func realBlob(id string) string {
	for _, c := range id {
		if c != '0' {
			return id
		}
	}
	return ""
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	s.st.Lock()
	f := s.st.File(r.URL.Query().Get("path"))
	s.st.Unlock()
	if f == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, f)
}

var blobID = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	path, id := r.URL.Query().Get("path"), r.URL.Query().Get("id")
	s.st.Lock()
	f := s.st.File(path)
	repo := s.st.Review.Spec.Repo
	s.st.Unlock()
	ctype, kind := mediaType(path)
	if f == nil || kind == "" || !blobID.MatchString(id) || (id != realBlob(f.OldBlob) && id != realBlob(f.NewBlob)) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	data, err := gitx.Blob(repo, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	// 音声の早送りや巻き戻しのために、範囲の要求（Range）にも応じる。
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func (s *Server) handleMark(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		Key  string `json:"key"`
		On   bool   `json:"on"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return nil, s.st.SetMark(req.Kind, req.Key, req.On, store.AuthorWorker) })
}

// do は状態を変える処理を鍵の中で行い、画面と待ち受けに知らせる。
func (s *Server) do(w http.ResponseWriter, fn func() (any, error)) {
	s.st.Lock()
	v, err := fn()
	s.st.Unlock()
	if err != nil {
		fail(w, err)
		return
	}
	s.changed()
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	writeJSON(w, v)
}

func (s *Server) handleNewThread(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string       `json:"kind"`
		Anchor store.Anchor `json:"anchor"`
		Body   string       `json:"body"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return s.st.NewThread(req.Kind, req.Anchor, req.Body, store.AuthorWorker) })
}

type bodyReq struct {
	Body string `json:"body"`
}

func (s *Server) handleAddMessage(w http.ResponseWriter, r *http.Request) {
	var req bodyReq
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return s.st.AddMessage(r.PathValue("id"), req.Body, store.AuthorWorker) })
}

func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	var req bodyReq
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return s.st.EditMessage(r.PathValue("id"), r.PathValue("mid"), req.Body) })
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) { return s.st.DeleteMessage(r.PathValue("id"), r.PathValue("mid")) })
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) { return s.st.Close(r.PathValue("id")) })
}

func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) {
		t := s.st.Threads[r.PathValue("id")]
		if t == nil {
			return nil, errNotFound
		}
		text := s.st.SinglePrompt(t)
		return map[string]string{"text": text}, s.st.MarkSent([]*store.Thread{t})
	})
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) { return s.st.SetPending(r.PathValue("id")) })
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) {
		text, path, err := s.st.ExportAll()
		return map[string]string{"text": text, "path": path}, err
	})
}

func (s *Server) handleSendAll(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) {
		n := 0
		for _, t := range s.st.ThreadList() {
			if t.State == store.StateClosed {
				continue
			}
			if _, err := s.st.SetPending(t.ID); err != nil {
				return nil, err
			}
			n++
		}
		return map[string]int{"queued": n}, nil
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	s.do(w, func() (any, error) { return nil, s.st.Refresh() })
}

type agentThreadReq struct {
	Thread string `json:"thread"`
	Body   string `json:"body"`
	Kind   string `json:"kind"`
	Anchor string `json:"anchor"`
}

func (s *Server) handleAgentReply(w http.ResponseWriter, r *http.Request) {
	var req agentThreadReq
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return s.st.AddMessage(req.Thread, req.Body, store.AuthorAgent) })
}

func (s *Server) handleAgentPost(w http.ResponseWriter, r *http.Request) {
	var req agentThreadReq
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) {
		a, err := s.st.ParseAnchor(req.Anchor)
		if err != nil {
			return nil, err
		}
		return s.st.NewThread(req.Kind, a, req.Body, store.AuthorAgent)
	})
}

func (s *Server) handleAgentReattach(w http.ResponseWriter, r *http.Request) {
	var req agentThreadReq
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) {
		a, err := s.st.ParseAnchor(req.Anchor)
		if err != nil {
			return nil, err
		}
		return s.st.Reattach(req.Thread, a)
	})
}

func (s *Server) handleAgentUnmark(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		Key  string `json:"key"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return nil, s.st.SetMark(req.Kind, req.Key, false, store.AuthorAgent) })
}

func (s *Server) handleAgentSections(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		fail(w, err)
		return
	}
	s.do(w, func() (any, error) { return nil, s.st.SetSections(data) })
}

var errNotFound = errors.New("見つかりません")
