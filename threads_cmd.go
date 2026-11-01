package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/review"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

// threadOut は、エージェント向けの命令が返すスレッドの形。
type threadOut struct {
	*store.Thread
	Where string `json:"where"`
	Found bool   `json:"found"`
}

func outOf(st *review.State, t *store.Thread) threadOut {
	where, ok := st.Describe(t.Anchor)
	return threadOut{Thread: t, Where: where, Found: ok}
}

// printResult は、スレッドを変えた命令の結果を 1 行の JSON で出す。
func printResult(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

// threadCall は、サーバーが動いていれば HTTP で、動いていなければ保存のファイルに直接、スレッドを変えて結果を出す。
func threadCall(repo, id, path string, body any, offline func(*review.State) (*store.Thread, error)) error {
	if c := connect(repo, id); c != nil {
		data, err := c.post(path, body)
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil
	}
	st, err := review.Load(repo, id)
	if err != nil {
		return err
	}
	t, err := offline(st)
	if err != nil {
		return err
	}
	return printResult(outOf(st, t))
}

func cmdThreads(args []string) error {
	if len(args) > 0 && args[0] == "delete" {
		return cmdThreadsDelete(args[1:])
	}
	a := newAgentFlags("threads")
	asJSON := a.fs.Bool("json", false, "JSON で出す")
	states := a.fs.String("state", "", "状態で絞る（unsent / sent / replied / closed。カンマで複数）")
	since := a.fs.String("since", "", "この時刻より後に変わったものだけ（例: 2026-11-01、2026-11-01T18:00）")
	if err := a.parse(args); err != nil {
		return err
	}
	st, err := review.Load(*a.repo, *a.review)
	if err != nil {
		return err
	}
	var from time.Time
	if *since != "" {
		if from, err = parseTime(*since); err != nil {
			return err
		}
	}
	var list []*store.Thread
	for _, t := range st.ThreadList() {
		if *states != "" && !inList(*states, t.State) {
			continue
		}
		if !from.IsZero() && t.UpdatedAt.Before(from) {
			continue
		}
		list = append(list, t)
	}
	if *asJSON {
		out := []threadOut{}
		for _, t := range list {
			out = append(out, outOf(st, t))
		}
		return printJSON(out)
	}
	fmt.Println(st.BatchText(list))
	return nil
}

func inList(csv, v string) bool {
	for _, x := range strings.Split(csv, ",") {
		if strings.TrimSpace(x) == v {
			return true
		}
	}
	return false
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("時刻を読めません: %s（例: 2026-11-01、2026-11-01T18:00）", s)
}

func splitList(csv string) []string {
	var out []string
	for _, x := range strings.Split(csv, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func cmdThreadsDelete(args []string) error {
	a := newAgentFlags("threads delete")
	ids := a.fs.String("thread", "", "消すスレッドの ID（カンマで複数）")
	states := a.fs.String("state", "", "この状態のスレッドを消す（unsent / sent / replied / closed。カンマで複数）")
	all := a.fs.Bool("all", false, "すべてのスレッドを消す")
	if err := a.parse(args); err != nil {
		return err
	}
	req := map[string]any{"threads": splitList(*ids), "states": splitList(*states), "all": *all}
	if c := connect(*a.repo, *a.review); c != nil {
		data, err := c.post("/api/agent/threads/delete", req)
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil
	}
	st, err := review.Load(*a.repo, *a.review)
	if err != nil {
		return err
	}
	n, err := st.DeleteThreads(splitList(*ids), splitList(*states), *all)
	if err != nil {
		return err
	}
	return printResult(map[string]int{"deleted": n})
}

func cmdReply(args []string) error {
	a := newAgentFlags("reply")
	thread := a.fs.String("thread", "", "スレッド ID")
	body := a.fs.String("body", "", "本文")
	file := a.fs.String("body-file", "", "本文のファイル（- なら標準入力）")
	if err := a.parse(args); err != nil {
		return err
	}
	text, err := readBodyFlag(*body, *file)
	if err != nil {
		return err
	}
	return threadCall(*a.repo, *a.review, "/api/agent/reply", map[string]string{"thread": *thread, "body": text},
		func(st *review.State) (*store.Thread, error) { return st.AddMessage(*thread, text, store.AuthorAgent) })
}

func cmdPost(args []string) error {
	a := newAgentFlags("post")
	anchor := a.fs.String("anchor", "review", "付け先")
	kind := a.fs.String("kind", "other", "種類")
	body := a.fs.String("body", "", "本文")
	file := a.fs.String("body-file", "", "本文のファイル（- なら標準入力）")
	if err := a.parse(args); err != nil {
		return err
	}
	text, err := readBodyFlag(*body, *file)
	if err != nil {
		return err
	}
	return threadCall(*a.repo, *a.review, "/api/agent/post", map[string]string{"anchor": *anchor, "kind": *kind, "body": text},
		func(st *review.State) (*store.Thread, error) {
			an, err := st.ParseAnchor(*anchor)
			if err != nil {
				return nil, err
			}
			return st.NewThread(*kind, an, text, store.AuthorAgent)
		})
}

func cmdReattach(args []string) error {
	a := newAgentFlags("reattach")
	thread := a.fs.String("thread", "", "スレッド ID")
	anchor := a.fs.String("anchor", "", "新しい付け先")
	if err := a.parse(args); err != nil {
		return err
	}
	return threadCall(*a.repo, *a.review, "/api/agent/reattach", map[string]string{"thread": *thread, "anchor": *anchor},
		func(st *review.State) (*store.Thread, error) {
			an, err := st.ParseAnchor(*anchor)
			if err != nil {
				return nil, err
			}
			return st.Reattach(*thread, an)
		})
}

func cmdClose(args []string) error {
	a := newAgentFlags("close")
	thread := a.fs.String("thread", "", "スレッド ID")
	if err := a.parse(args); err != nil {
		return err
	}
	return threadCall(*a.repo, *a.review, "/api/agent/close", map[string]string{"thread": *thread},
		func(st *review.State) (*store.Thread, error) { return st.AgentClose(*thread) })
}

func cmdWait(args []string) error {
	a := newAgentFlags("wait")
	asJSON := a.fs.Bool("json", false, "結果を JSON で出す（status・threads・prompt）")
	if err := a.parse(args); err != nil {
		return err
	}
	stop := func(msg string) error {
		if *asJSON {
			printResult(map[string]string{"status": "no-server"})
		}
		return exitError{4, msg}
	}
	c := connect(*a.repo, *a.review)
	if c == nil {
		return stop("Sedi Review のサーバーが動いていません（画面が閉じられた可能性があります）。待ち受けを終えてください。")
	}
	data, err := c.get("/api/agent/poll")
	if err != nil {
		if connect(*a.repo, *a.review) == nil {
			return stop("Sedi Review のサーバーが終わりました。待ち受けを終えてください。")
		}
		return err
	}
	var res struct {
		Status string `json:"status"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	if *asJSON {
		os.Stdout.Write(data)
	}
	switch res.Status {
	case "messages":
		if !*asJSON {
			fmt.Println(res.Prompt)
			fmt.Println("\n対応したら、もう一度 wait を動かして待ち受けを続けてください。")
		}
		return nil
	case "timeout":
		return exitError{3, "時間切れです。渡すものはありません。もう一度 wait を動かしてください。"}
	}
	return fmt.Errorf("予期しない応答です: %s", strings.TrimSpace(string(data)))
}
