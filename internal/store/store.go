// Package store はレビューの保存の形と読み書きを持つ。保存の場所はリポジトリの外。
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Spec struct {
	Repo      string   `json:"repo"`
	Base      string   `json:"base"`
	Target    string   `json:"target"`
	ThreeDot  bool     `json:"threeDot"`
	Rule      string   `json:"rule"`
	Pathspecs []string `json:"pathspecs"`
}

type Review struct {
	ID           string    `json:"id"`
	Spec         Spec      `json:"spec"`
	BaseCommit   string    `json:"baseCommit"`
	TargetCommit string    `json:"targetCommit"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Section struct {
	Name     string      `json:"name"`
	Intent   string      `json:"intent,omitempty"`
	Checks   []string    `json:"checks,omitempty"`
	Files    []string    `json:"files,omitempty"`
	Hunks    []string    `json:"hunks,omitempty"`
	Lines    []LineRange `json:"lines,omitempty"`
	Children []*Section  `json:"children,omitempty"`
}

// LineRange はファイルの行の範囲。大きな hunk（新しいファイルなど）の一部だけをセクションに入れるときに使う。
type LineRange struct {
	Path string `json:"path"`
	Side string `json:"side,omitempty"` // new（既定）か old
	From int    `json:"from"`
	To   int    `json:"to"`
}

type Mark struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
}

type Marks struct {
	Files    map[string]Mark `json:"files"`
	Sections map[string]Mark `json:"sections"`
}

const (
	AnchorReview  = "review"
	AnchorSection = "section"
	AnchorFile    = "file"
	AnchorHunk    = "hunk"
	AnchorLine    = "line"
	AnchorLines   = "lines" // パスと行番号で指す行。hunk は表示のたびに決める
)

type Anchor struct {
	Type    string   `json:"type"`
	Section string   `json:"section,omitempty"`
	Path    string   `json:"path,omitempty"`
	Hunk    string   `json:"hunk,omitempty"`
	Side    string   `json:"side,omitempty"` // old new
	Start   int      `json:"start,omitempty"`
	End     int      `json:"end,omitempty"`
	From    int      `json:"from,omitempty"`
	To      int      `json:"to,omitempty"`
	Text    []string `json:"text,omitempty"`
}

const (
	AuthorWorker = "worker"
	AuthorAgent  = "agent"
)

type Message struct {
	ID     string    `json:"id"`
	Author string    `json:"author"`
	Body   string    `json:"body"`
	Sent   bool      `json:"sent"`
	At     time.Time `json:"at"`
}

const (
	StateUnsent  = "unsent"
	StateSent    = "sent"
	StateReplied = "replied"
	StateClosed  = "closed"
)

var Kinds = map[string]string{
	"instruction": "指示",
	"question":    "質問",
	"reconsider":  "再考",
	"other":       "その他",
}

type Thread struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Anchor  Anchor `json:"anchor"`
	State   string `json:"state"`
	Pending bool   `json:"pending"`
	// AgentMayClose は、作業者がエージェントに閉じることを許したか。
	AgentMayClose bool      `json:"agentMayClose,omitempty"`
	Messages      []Message `json:"messages"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type ServerInfo struct {
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"startedAt"`
}

// DataDirName はリポジトリ直下に置く、git から外した保存のフォルダ。
const DataDirName = ".sedi-review"

// Root はリポジトリの保存のフォルダを返す。
func Root(repo string) string {
	return filepath.Join(repo, DataDirName)
}

func ReviewsDir(repo string) string {
	return filepath.Join(Root(repo), "reviews")
}

// ReviewID は同じ指定に同じ ID を返す。保存はリポジトリごとなので、リポジトリのパスは入れない。
func ReviewID(s Spec) string {
	parts := []string{s.Base, s.Target, fmt.Sprint(s.ThreeDot), s.Rule}
	parts = append(parts, s.Pathspecs...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}

func Dir(repo, id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return "", fmt.Errorf("レビューの ID が不正です: %q", id)
	}
	return filepath.Join(ReviewsDir(repo), id), nil
}

func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// WriteJSON は一時ファイルに書いてから名前を変える。
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ReadJSON は無いファイルを空として扱い、ok=false を返す。
func ReadJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

func LoadThreads(dir string) (map[string]*Thread, error) {
	threads := map[string]*Thread{}
	entries, err := os.ReadDir(filepath.Join(dir, "threads"))
	if errors.Is(err, fs.ErrNotExist) {
		return threads, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var t Thread
		if _, err := ReadJSON(filepath.Join(dir, "threads", e.Name()), &t); err != nil {
			return nil, err
		}
		threads[t.ID] = &t
	}
	return threads, nil
}

func SaveThread(dir string, t *Thread) error {
	return WriteJSON(filepath.Join(dir, "threads", t.ID+".json"), t)
}

func DeleteThread(dir, id string) error {
	err := os.Remove(filepath.Join(dir, "threads", id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
