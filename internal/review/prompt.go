package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/store"
)

// ExePath はエージェントに渡す文に書く、この実行ファイルのパス。
var ExePath = "sedi-review"

var stateLabels = map[string]string{
	store.StateUnsent:  "未送付",
	store.StateSent:    "送付済み",
	store.StateReplied: "返事あり",
	store.StateClosed:  "閉じる",
}

const kindRules = `種類ごとの対応:
- 指示: 求められた変更をする。しない場合は理由を返事に書く
- 質問: 答えるだけ。コードは変えない
- 再考: 考え直した結果と代わりの案を返事に書く。コードを変えるかは、作業者が返事を見て決める
- その他: 本文から判断する`

func (s *State) header() string {
	sp := s.Review.Spec
	op := "..."
	if !sp.ThreeDot {
		op = " "
	}
	return fmt.Sprintf("Sedi Review のレビュー %s（%s の %s%s%s、%s..%s）",
		s.Review.ID, sp.Repo, sp.Base, op, sp.Target, short(s.Review.BaseCommit), short(s.Review.TargetCommit))
}

func short(c string) string {
	if len(c) > 10 {
		return c[:10]
	}
	return c
}

func (s *State) cmd(args string) string {
	return fmt.Sprintf("\"%s\" %s --repo \"%s\" --review %s", filepath.ToSlash(ExePath), args, s.Review.Spec.Repo, s.Review.ID)
}

func (s *State) howToReply(tid string) string {
	thread := "<スレッドID>"
	if tid != "" {
		thread = tid
	}
	return strings.Join([]string{
		"返事のしかた:",
		"- チャットで返事をし、同じ内容を道具にも書く（本文は標準入力から UTF-8 で渡す）:",
		"  " + s.cmd("reply") + " --thread " + thread + " --body-file -",
		"- 付け先が見つからないときは、行の中身などを手がかりに探し直し、付け直してよい:",
		"  " + s.cmd("reattach") + " --thread " + thread + " --anchor <付け先>",
		"- 確認済みの印は外すことだけができる:",
		"  " + s.cmd("unmark") + " --file <パス>   または   --section <セクションの鍵>",
		"- 付け先の書き方: review / section:<鍵> / file:<パス> / hunk:<ID> / line:<ID>:old|new:<始め>-<終わり>（hunk の中の 0 から数えた位置）",
		"- セクションを作る（作り直す）ときは、hunk の一覧を見て、JSON を標準入力から渡す:",
		"  " + s.cmd("hunks"),
		"  " + s.cmd("sections") + " set -",
		`  形: [{"name": "名前", "intent": "意図", "checks": ["確かめる点"], "files": ["丸ごと入れるファイルのパス"], "hunks": ["他のファイルから入れる hunk の ID"], "children": [ ...同じ形... ]}]`,
		"  並びの順が読む順。1 つの hunk を複数のセクションに入れてよい。入らなかった hunk は未分類になる",
	}, "\n")
}

// ThreadBlock は 1 つのスレッドを渡す形の文にする。
func (s *State) ThreadBlock(t *store.Thread) string {
	var b strings.Builder
	where, ok := s.Describe(t.Anchor)
	if !ok {
		where += "（付け先なし）"
	}
	fmt.Fprintf(&b, "## スレッド %s\n", t.ID)
	fmt.Fprintf(&b, "- 種類: %s\n- 状態: %s\n- 付け先: %s\n", store.Kinds[t.Kind], stateLabels[t.State], where)
	if len(t.Anchor.Text) > 0 {
		b.WriteString("- 対象の行の写し:\n```\n")
		for _, l := range t.Anchor.Text {
			b.WriteString(strings.TrimSuffix(l, "\r"))
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}
	b.WriteString("- やり取り:\n")
	for _, m := range t.Messages {
		who := "作業者"
		if m.Author == store.AuthorAgent {
			who = "エージェント"
		}
		fmt.Fprintf(&b, "  - %s:\n", who)
		for _, l := range strings.Split(strings.TrimRight(m.Body, "\n"), "\n") {
			b.WriteString("    > " + l + "\n")
		}
	}
	return b.String()
}

// SinglePrompt は個別のコピーで貼る文。
func (s *State) SinglePrompt(t *store.Thread) string {
	return strings.Join([]string{s.header() + " のメッセージです。", s.ThreadBlock(t), kindRules, s.howToReply(t.ID)}, "\n\n")
}

// BatchText は複数のスレッドをまとめた文。一括の書き出しと待ち受けで使う。
func (s *State) BatchText(threads []*store.Thread) string {
	parts := []string{
		s.header() + " のメッセージ（" + fmt.Sprint(len(threads)) + " 件）です。",
		"状態が「返事あり」「閉じる」のスレッドに、新しい作業者のメッセージが無ければ、二度答えなくてよい。",
		kindRules,
		s.howToReply(""),
	}
	for _, t := range threads {
		parts = append(parts, s.ThreadBlock(t))
	}
	return strings.Join(parts, "\n\n")
}

// ExportAll は全スレッドを書き出し、貼るための短い文を返す。
func (s *State) ExportAll() (string, string, error) {
	threads := s.ThreadList()
	path := filepath.Join(s.Dir, "exports", "messages-"+time.Now().Format("20060102-150405")+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, []byte(s.BatchText(threads)), 0o600); err != nil {
		return "", "", err
	}
	prompt := strings.Join([]string{
		s.header() + " のメッセージを書き出しました（" + fmt.Sprint(len(threads)) + " 件）。",
		"次のファイルを読み、各スレッドに種類に従って対応してください:",
		filepath.ToSlash(path),
	}, "\n")
	return prompt, path, s.MarkSent(threads)
}

// Overview はエージェントに渡す hunk の一覧。
func (s *State) Overview() string {
	var b strings.Builder
	b.WriteString(s.header() + "\n\n")
	for _, f := range s.Files {
		status := f.Status
		if f.OldPath != "" {
			status += " from " + f.OldPath
		}
		if f.Binary {
			status += " binary"
		}
		fmt.Fprintf(&b, "%s (%s)\n", f.Path, status)
		for _, h := range f.Hunks {
			fmt.Fprintf(&b, "  %s h%d @@ -%d,%d +%d,%d @@ +%d -%d %s\n", h.ID, h.Num, h.OldStart, h.OldCount, h.NewStart, h.NewCount, h.Adds, h.Dels, h.Context)
		}
	}
	return b.String()
}
