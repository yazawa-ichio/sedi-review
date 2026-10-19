// Package server は 127.0.0.1 の一時サーバー。画面とエージェントの CLI の両方を受ける。
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/review"
	"github.com/yazawa-ichio/sedi-review/internal/store"
	"github.com/yazawa-ichio/sedi-review/web"
)

const (
	idleAfterClose = 30 * time.Second
	idleNeverOpen  = 10 * time.Minute
	maxPoll        = 25 * time.Minute
)

// ToolLicense は Sedi Review 自体のライセンスの文。実行ファイルに埋め込んだ LICENSE を main が入れる。
var ToolLicense = ""

type Server struct {
	st    *review.State
	token string
	port  int

	mu            sync.Mutex
	clients       map[chan string]struct{}
	everConnected bool
	lastClient    time.Time
	started       time.Time
	pollers       int
	deliveredAt   time.Time
	wake          chan struct{}
}

func New(st *review.State) *Server {
	return &Server{
		st:      st,
		token:   store.NewID() + store.NewID(),
		clients: map[chan string]struct{}{},
		started: time.Now(),
		wake:    make(chan struct{}),
	}
}

// Listen は待ち受けを始め、URL を返す。
func (s *Server) Listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, "", err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	info := store.ServerInfo{Port: s.port, PID: os.Getpid(), Token: s.token, StartedAt: s.started}
	if err := store.WriteJSON(filepath.Join(s.st.Dir, "server.json"), info); err != nil {
		ln.Close()
		return nil, "", err
	}
	return ln, fmt.Sprintf("http://127.0.0.1:%d/", s.port), nil
}

// Serve は画面が閉じられて猶予がたつか、ctx が終わるまで動く。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.watchIdle(ctx, cancel)
	go func() {
		<-ctx.Done()
		shut, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		srv.Shutdown(shut)
	}()
	err := srv.Serve(ln)
	os.Remove(filepath.Join(s.st.Dir, "server.json"))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) watchIdle(ctx context.Context, stop func()) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		idle := len(s.clients) == 0 &&
			((s.everConnected && time.Since(s.lastClient) > idleAfterClose) ||
				(!s.everConnected && time.Since(s.started) > idleNeverOpen))
		s.mu.Unlock()
		if idle {
			stop()
			return
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(web.Files, "static")
	if dir := os.Getenv("SEDI_REVIEW_WEB"); dir != "" {
		static = os.DirFS(dir)
	}
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /LICENSE.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, ToolLicense)
	})

	mux.HandleFunc("GET /api/review", s.handleSummary)
	mux.HandleFunc("GET /api/file", s.handleFile)
	mux.HandleFunc("GET /api/blob", s.handleBlob)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	mux.HandleFunc("POST /api/mark", s.page(s.handleMark))
	mux.HandleFunc("POST /api/threads", s.page(s.handleNewThread))
	mux.HandleFunc("POST /api/threads/{id}/messages", s.page(s.handleAddMessage))
	mux.HandleFunc("PATCH /api/threads/{id}/messages/{mid}", s.page(s.handleEditMessage))
	mux.HandleFunc("DELETE /api/threads/{id}/messages/{mid}", s.page(s.handleDeleteMessage))
	mux.HandleFunc("POST /api/threads/{id}/close", s.page(s.handleClose))
	mux.HandleFunc("POST /api/threads/{id}/copy", s.page(s.handleCopy))
	mux.HandleFunc("POST /api/threads/{id}/send", s.page(s.handleSend))
	mux.HandleFunc("POST /api/export", s.page(s.handleExport))
	mux.HandleFunc("POST /api/send-all", s.page(s.handleSendAll))
	mux.HandleFunc("POST /api/refresh", s.page(s.handleRefresh))

	mux.HandleFunc("GET /api/agent/ping", s.agent(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]string{"id": s.st.Review.ID}) }))
	mux.HandleFunc("GET /api/agent/poll", s.agent(s.handlePoll))
	mux.HandleFunc("POST /api/agent/reply", s.agent(s.handleAgentReply))
	mux.HandleFunc("POST /api/agent/post", s.agent(s.handleAgentPost))
	mux.HandleFunc("POST /api/agent/reattach", s.agent(s.handleAgentReattach))
	mux.HandleFunc("POST /api/agent/unmark", s.agent(s.handleAgentUnmark))
	mux.HandleFunc("POST /api/agent/sections", s.agent(s.handleAgentSections))
	mux.HandleFunc("POST /api/agent/refresh", s.agent(s.handleRefresh))
	return s.guard(mux)
}

// guard は 127.0.0.1 宛て以外の Host を断る（DNS リバインディング対策）。
func (s *Server) guard(next http.Handler) http.Handler {
	allowed := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", s.port): true,
		fmt.Sprintf("localhost:%d", s.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// page は画面からの書き込みを、同じ Origin と独自の見出しに限る。
func (s *Server) page(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sedi") != "1" {
			http.Error(w, "missing header", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) agent(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Sedi-Token")), []byte(s.token)) != 1 {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func readBody(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("要求の JSON を読めません: %w", err)
	}
	return nil
}

// changed は画面に知らせ、待ち受けを起こす。
func (s *Server) changed() {
	s.mu.Lock()
	for c := range s.clients {
		select {
		case c <- "changed":
		default:
		}
	}
	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no stream", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	c := make(chan string, 4)
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.everConnected = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.lastClient = time.Now()
		s.mu.Unlock()
	}()
	fmt.Fprint(w, ": open\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-c:
			fmt.Fprintf(w, "data: %s\n\n", ev)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		fl.Flush()
	}
}

func (s *Server) presence() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := map[string]any{"attached": s.pollers > 0}
	if !s.deliveredAt.IsZero() {
		p["deliveredAt"] = s.deliveredAt
	}
	return p
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	timeout := maxPoll
	if v, err := strconv.Atoi(r.URL.Query().Get("timeout")); err == nil && v > 0 && time.Duration(v)*time.Second < maxPoll {
		timeout = time.Duration(v) * time.Second
	}
	s.mu.Lock()
	s.pollers++
	s.mu.Unlock()
	s.changed()
	defer func() {
		s.mu.Lock()
		s.pollers--
		s.mu.Unlock()
		s.changed()
	}()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		s.st.Lock()
		pending := s.st.PendingThreads()
		var text string
		if len(pending) > 0 {
			text = s.st.BatchText(pending)
		}
		s.st.Unlock()
		if len(pending) > 0 {
			ids := make([]string, len(pending))
			for i, t := range pending {
				ids[i] = t.ID
			}
			data, _ := json.Marshal(map[string]any{"status": "messages", "threads": ids, "prompt": text})
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if _, err := w.Write(append(data, '\n')); err != nil {
				return
			}
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			if r.Context().Err() != nil {
				return
			}
			s.st.Lock()
			err := s.st.MarkSent(pending)
			s.st.Unlock()
			s.mu.Lock()
			s.deliveredAt = time.Now()
			s.mu.Unlock()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			return
		}
		s.mu.Lock()
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			writeJSON(w, map[string]string{"status": "timeout"})
			return
		case <-wake:
		}
	}
}

// mediaTypes は画面で出せる画像と音声の形式。ブラウザが扱えるものだけ。
var mediaTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
}

// mediaType は形式と、"image" か "audio" を返す。どちらでもなければ空。
func mediaType(path string) (string, string) {
	ctype := mediaTypes[strings.ToLower(filepath.Ext(path))]
	kind, _, _ := strings.Cut(ctype, "/")
	return ctype, kind
}
