// Sedi Review: AI が書いた大きな差分を、意味のセクションに分けて読むための道具。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"

	"github.com/yazawa-ichio/sedi-review/internal/gitx"
	"github.com/yazawa-ichio/sedi-review/internal/review"
	"github.com/yazawa-ichio/sedi-review/internal/server"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

const usage = `使い方（リポジトリの中で。別の場所からは --repo PATH）:
  sedi-review init [--base REF] [--rule NAME] [--two-dot] [--force]
  sedi-review skill install|uninstall --agent claude|codex[,...] [--scope user|project] [--force]
  sedi-review skill status [--agent ...]
  sedi-review open [--base REF] [--target REF] [--two-dot] [--rule NAME] [--port N] [--open] [-- PATHSPEC...]
  sedi-review list
  sedi-review refresh  --review ID

エージェント向け（どれも --repo PATH を付けられる）:
  sedi-review hunks    --review ID [--json]
  sedi-review sections --review ID set FILE|-
  sedi-review sections --review ID show
  sedi-review threads  --review ID [--json]
  sedi-review reply    --review ID --thread TID (--body TEXT | --body-file FILE|-)
  sedi-review post     --review ID --anchor ANCHOR [--kind KIND] (--body TEXT | --body-file FILE|-)
  sedi-review reattach --review ID --thread TID --anchor ANCHOR
  sedi-review unmark   --review ID (--file PATH | --section KEY)
  sedi-review wait     --review ID

ANCHOR: review | section:<鍵> | file:<パス> | hunk:<ID> | line:<ID>:old|new:<始め>-<終わり>
KIND:   instruction | question | reconsider | other
`

func main() {
	if exe, err := os.Executable(); err == nil {
		review.ExePath = exe
	}
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"init": cmdInit, "skill": cmdSkill, "open": cmdOpen, "list": cmdList, "refresh": cmdRefresh,
		"hunks": cmdHunks, "sections": cmdSections, "threads": cmdThreads,
		"reply": cmdReply, "post": cmdPost, "reattach": cmdReattach, "unmark": cmdUnmark, "wait": cmdWait,
	}
	fn, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := fn(os.Args[2:]); err != nil {
		var ex exitError
		if errors.As(err, &ex) {
			fmt.Fprintln(os.Stderr, ex.msg)
			os.Exit(ex.code)
		}
		fmt.Fprintln(os.Stderr, "sedi-review:", err)
		os.Exit(1)
	}
}

type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	base := fs.String("base", "", "open の既定の比べる元（省くと main か master）")
	rule := fs.String("rule", "", "既定のルール（省くと unity）")
	twoDot := fs.Bool("two-dot", false, "既定を A...B ではなく A B にする")
	force := fs.Bool("force", false, "設定のファイルがあっても書き直す")
	repo := fs.String("repo", ".", "リポジトリ")
	fs.Parse(args)
	done, err := review.Init(*repo, review.InitOptions{Base: *base, Rule: *rule, TwoDot: *twoDot, Force: *force})
	for _, d := range done {
		fmt.Println(d)
	}
	return err
}

func cmdOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	base := fs.String("base", "", "比べる元（省くと "+review.ConfigFile+" の base）")
	target := fs.String("target", "HEAD", "比べる先（コミットかブランチ）")
	twoDot := fs.Bool("two-dot", false, "A...B ではなく A B で比べる（省くと "+review.ConfigFile+" の twoDot）")
	rule := fs.String("rule", "", "名前を付けたルール（省くと "+review.ConfigFile+" の rule、無ければ unity）")
	port := fs.Int("port", 0, "待ち受けるポート（0 なら空きを使う）")
	open := fs.Bool("open", false, "ブラウザで開く")
	repo := fs.String("repo", ".", "リポジトリ")
	fs.Parse(args)
	top, err := review.Top(*repo)
	if err != nil {
		return err
	}
	cfg, err := review.LoadConfig(top)
	if err != nil {
		return err
	}
	if *base == "" {
		*base = cfg.Base
	}
	if *base == "" {
		return fmt.Errorf("--base を指定するか、%s に base を書いてください", review.ConfigFile)
	}
	threeDot := !cfg.TwoDot
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "two-dot" {
			threeDot = !*twoDot
		}
	})
	st, err := review.Create(store.Spec{Repo: top, Base: *base, Target: *target, ThreeDot: threeDot, Rule: *rule, Pathspecs: fs.Args()})
	if err != nil {
		return err
	}
	if c := connect(top, st.Review.ID); c != nil {
		fmt.Printf("Sedi Review は既に動いています: %s\nレビュー ID: %s\n", c.base+"/", st.Review.ID)
		return nil
	}
	srv := server.New(st)
	ln, url, err := srv.Listen(*port)
	if err != nil {
		return err
	}
	hunks := 0
	for _, f := range st.Files {
		hunks += len(f.Hunks)
	}
	fmt.Printf("Sedi Review: %s\nレビュー ID: %s\n差分: %d ファイル、%d hunk\n画面を閉じると終わります（Ctrl+C でも止まります）。\n", url, st.Review.ID, len(st.Files), hunks)
	if *open {
		openBrowser(url)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return srv.Serve(ctx, ln)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	gitx.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "ブラウザを開けませんでした:", err)
	}
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	repo := fs.String("repo", ".", "リポジトリ")
	fs.Parse(args)
	list, err := review.ListReviews(*repo)
	if err != nil {
		return err
	}
	for _, r := range list {
		op := "..."
		if !r.Spec.ThreeDot {
			op = " "
		}
		fmt.Printf("%s  %s  %s%s%s  %s\n", r.ID, r.Spec.Repo, r.Spec.Base, op, r.Spec.Target, r.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

// agentFlags はエージェント向けの命令に共通の指定。
type agentFlags struct {
	fs     *flag.FlagSet
	review *string
	repo   *string
}

func newAgentFlags(name string) *agentFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	return &agentFlags{fs: fs, review: fs.String("review", "", "レビュー ID"), repo: fs.String("repo", ".", "リポジトリ")}
}

func (a *agentFlags) parse(args []string) error {
	a.fs.Parse(args)
	if *a.review == "" {
		return fmt.Errorf("--review を指定してください")
	}
	return nil
}

// withState はサーバーが動いていれば HTTP で、動いていなければ保存のファイルに直接書く。
func withState(repo, id string, viaServer func(*client) error, offline func(*review.State) error) error {
	if c := connect(repo, id); c != nil && viaServer != nil {
		return viaServer(c)
	}
	st, err := review.Load(repo, id)
	if err != nil {
		return err
	}
	return offline(st)
}

func readBodyFlag(body, file string) (string, error) {
	switch {
	case body != "" && file != "":
		return "", fmt.Errorf("--body と --body-file は一緒に使えません")
	case body != "":
		return body, nil
	case file == "-":
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	case file != "":
		data, err := os.ReadFile(file)
		return string(data), err
	}
	return "", fmt.Errorf("--body か --body-file を指定してください")
}

func cmdRefresh(args []string) error {
	a := newAgentFlags("refresh")
	if err := a.parse(args); err != nil {
		return err
	}
	return withState(*a.repo, *a.review,
		func(c *client) error { _, err := c.post("/api/agent/refresh", map[string]any{}); return err },
		func(st *review.State) error { return st.Refresh() })
}

func cmdHunks(args []string) error {
	a := newAgentFlags("hunks")
	asJSON := a.fs.Bool("json", false, "JSON で出す")
	if err := a.parse(args); err != nil {
		return err
	}
	st, err := review.Load(*a.repo, *a.review)
	if err != nil {
		return err
	}
	if !*asJSON {
		fmt.Print(st.Overview())
		return nil
	}
	type hunk struct {
		ID, Context                                 string
		Num, OldStart, OldCount, NewStart, NewCount int
		Adds, Dels                                  int
	}
	type file struct {
		Path, OldPath, Status string
		Binary                bool
		Hunks                 []hunk
	}
	var out []file
	for _, f := range st.Files {
		ff := file{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Binary: f.Binary}
		for _, h := range f.Hunks {
			ff.Hunks = append(ff.Hunks, hunk{h.ID, h.Context, h.Num, h.OldStart, h.OldCount, h.NewStart, h.NewCount, h.Adds, h.Dels})
		}
		out = append(out, ff)
	}
	return printJSON(out)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdSections(args []string) error {
	a := newAgentFlags("sections")
	if err := a.parse(args); err != nil {
		return err
	}
	rest := a.fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("set FILE|- か show を指定してください")
	}
	switch rest[0] {
	case "show":
		st, err := review.Load(*a.repo, *a.review)
		if err != nil {
			return err
		}
		hunks, files := st.Unassigned()
		return printJSON(map[string]any{"sections": st.RawSections(), "unassigned": map[string]any{"hunks": hunks, "files": files}})
	case "set":
		if len(rest) < 2 {
			return fmt.Errorf("set の後に FILE か - を指定してください")
		}
		var data []byte
		var err error
		if rest[1] == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(rest[1])
		}
		if err != nil {
			return err
		}
		return withState(*a.repo, *a.review,
			func(c *client) error { _, err := c.postRaw("/api/agent/sections", data); return err },
			func(st *review.State) error { return st.SetSections(data) })
	}
	return fmt.Errorf("set か show を指定してください: %s", rest[0])
}

func cmdThreads(args []string) error {
	a := newAgentFlags("threads")
	asJSON := a.fs.Bool("json", false, "JSON で出す")
	if err := a.parse(args); err != nil {
		return err
	}
	st, err := review.Load(*a.repo, *a.review)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(st.ThreadList())
	}
	fmt.Println(st.BatchText(st.ThreadList()))
	return nil
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
	return withState(*a.repo, *a.review,
		func(c *client) error {
			_, err := c.post("/api/agent/reply", map[string]string{"thread": *thread, "body": text})
			return err
		},
		func(st *review.State) error { _, err := st.AddMessage(*thread, text, store.AuthorAgent); return err })
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
	return withState(*a.repo, *a.review,
		func(c *client) error {
			_, err := c.post("/api/agent/post", map[string]string{"anchor": *anchor, "kind": *kind, "body": text})
			return err
		},
		func(st *review.State) error {
			an, err := st.ParseAnchor(*anchor)
			if err != nil {
				return err
			}
			_, err = st.NewThread(*kind, an, text, store.AuthorAgent)
			return err
		})
}

func cmdReattach(args []string) error {
	a := newAgentFlags("reattach")
	thread := a.fs.String("thread", "", "スレッド ID")
	anchor := a.fs.String("anchor", "", "新しい付け先")
	if err := a.parse(args); err != nil {
		return err
	}
	return withState(*a.repo, *a.review,
		func(c *client) error {
			_, err := c.post("/api/agent/reattach", map[string]string{"thread": *thread, "anchor": *anchor})
			return err
		},
		func(st *review.State) error {
			an, err := st.ParseAnchor(*anchor)
			if err != nil {
				return err
			}
			_, err = st.Reattach(*thread, an)
			return err
		})
}

func cmdUnmark(args []string) error {
	a := newAgentFlags("unmark")
	file := a.fs.String("file", "", "ファイルのパス")
	section := a.fs.String("section", "", "セクションの鍵")
	if err := a.parse(args); err != nil {
		return err
	}
	kind, key := "file", *file
	if *section != "" {
		kind, key = "section", *section
	}
	if key == "" || (*file != "" && *section != "") {
		return fmt.Errorf("--file か --section のどちらか 1 つを指定してください")
	}
	return withState(*a.repo, *a.review,
		func(c *client) error {
			_, err := c.post("/api/agent/unmark", map[string]string{"kind": kind, "key": key})
			return err
		},
		func(st *review.State) error { return st.SetMark(kind, key, false, store.AuthorAgent) })
}

func cmdWait(args []string) error {
	a := newAgentFlags("wait")
	if err := a.parse(args); err != nil {
		return err
	}
	c := connect(*a.repo, *a.review)
	if c == nil {
		return exitError{4, "Sedi Review のサーバーが動いていません（画面が閉じられた可能性があります）。待ち受けを終えてください。"}
	}
	data, err := c.get("/api/agent/poll")
	if err != nil {
		if connect(*a.repo, *a.review) == nil {
			return exitError{4, "Sedi Review のサーバーが終わりました。待ち受けを終えてください。"}
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
	switch res.Status {
	case "messages":
		fmt.Println(res.Prompt)
		fmt.Println("\n対応したら、もう一度 wait を動かして待ち受けを続けてください。")
		return nil
	case "timeout":
		return exitError{3, "時間切れです。渡すものはありません。もう一度 wait を動かしてください。"}
	}
	return fmt.Errorf("予期しない応答です: %s", strings.TrimSpace(string(data)))
}
