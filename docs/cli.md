# Sedi Review の命令

どの命令も `--repo PATH` でリポジトリを指定できます。省くと今の場所です。

## 作業者が使う命令

```
sedi-review init [--base REF] [--rule NAME] [--two-dot] [--force]
sedi-review open [--base REF] [--target REF] [--two-dot] [--rule NAME] [--port N] [--open] [-- PATHSPEC...]
sedi-review list
sedi-review refresh --review ID
```

| 命令 | すること |
|---|---|
| `init` | 保存のフォルダ `.sedi-review/` と設定 `.sedi-review.json` を用意する。設定があれば書き直さない（`--force` で書き直す） |
| `open` | レビューを開き、画面の URL を出す。同じ指定のレビューがあれば続きから開く。既に動いていれば、その URL を出す |
| `list` | このリポジトリのレビューを出す |
| `refresh` | 同じ指定のまま差分を取り直す。中身の変わったファイルとセクションの確認済みを外す |

```
sedi-review version
```

版を出す。`go install ...@v0.1.0` で入れたものはその版、手元で作ったものはコミットを出す（`--version` でも同じ）。

### 開く指定の例

```bash
sedi-review open --target feature/save                       # 設定の base と比べる
sedi-review open --base 1a2b3c4~1 --target 1a2b3c4            # 1 つのコミットだけ
sedi-review open --base main --target work -- '*.cs'          # コードだけ（C#）
sedi-review open --base main --target work -- Assets/App ':(exclude)Assets/App/Generated'
sedi-review open --base main --target work --rule code        # .sedi-review.json の rules.code
```

`--` の後は git の pathspec です。いつも使う絞り方は、`.sedi-review.json` に名前を付けて書けます:

```json
{ "base": "main", "rule": "unity", "rules": { "unity": [":(exclude,glob)**/*.cs.meta"], "code": [":(glob)**/*.cs"] } }
```

## skill を入れる命令

```
sedi-review skill install   --agent claude|codex[,...] [--scope user|project] [--force] [--repo PATH]
sedi-review skill uninstall --agent claude|codex[,...] [--scope user|project] [--force] [--repo PATH]
sedi-review skill status    [--agent claude|codex[,...]] [--repo PATH]
```

| 命令 | すること |
|---|---|
| `skill install` | 実行ファイルに埋め込んだ skill を、エージェントの置き場所に写す。違う中身（手で直したもの、古い版）が入っていれば止まる。`--force` で置き換える |
| `skill uninstall` | skill のファイルを消し、空になったフォルダを消す。利用者が足したファイルは残す。中身が違えば止まる。`--force` で消す |
| `skill status` | user と project の各置き場所に、入っているか、この版と同じかを出す |

置き場所は、Claude Code が `~/.claude/skills/sedi-review/`（`CLAUDE_CONFIG_DIR` があればその下）か `<リポジトリ>/.claude/skills/sedi-review/`、
Codex が `~/.agents/skills/sedi-review/` か `<リポジトリ>/.agents/skills/sedi-review/` です。

## エージェント向けの命令

```
sedi-review hunks    --review ID [--files] [--json] [PATH...]
sedi-review sections --review ID set FILE|-
sedi-review sections --review ID show
sedi-review threads  --review ID [--json]
sedi-review reply    --review ID --thread TID (--body TEXT | --body-file FILE|-)
sedi-review post     --review ID --anchor ANCHOR [--kind KIND] (--body TEXT | --body-file FILE|-)
sedi-review reattach --review ID --thread TID --anchor ANCHOR
sedi-review unmark   --review ID (--file PATH | --section KEY)
sedi-review wait     --review ID
```

| 命令 | すること |
|---|---|
| `hunks` | ファイルと hunk の一覧を出す。各 hunk は `<ID> h<通し番号> @@ ... @@ +追加 -削除 <関数の文脈>`。`--files` でファイルごとの hunk の数と行数だけを出す。`PATH` で絞る（下） |
| `sections set` | セクションの JSON を確かめてから取り込む（形は [仕組み](design.md) の 7 章） |
| `sections show` | 取り込んだセクションと、未分類の hunk・hunk の残りの範囲・ファイルを出す |
| `threads` | 全スレッドを、渡す文の形で出す |
| `reply` | スレッドに返事を書く |
| `post` | エージェントから新しいスレッドを始める。`KIND` の既定は `other` |
| `reattach` | スレッドの付け先を付け直す |
| `unmark` | 確認済みの印を外す。付けることはできない |
| `wait` | 画面から「送る」で渡されたメッセージを待つ |

本文は `--body-file -` で標準入力から UTF-8 で渡せます。

### `hunks` の PATH

- フォルダかファイルのパス: そのパスと、その下（例: `Assets/App`）。`/` を含まない名前は、ファイル名とも比べる（例: `Save.cs`）
- `*` `?` `[` を含む glob: `/` を含めばパス全体に、含まなければファイル名に当てる（例: `'*.cs'`、`'Assets/*/Save.cs'`）
- 並べると、どれかに合うファイルを出す。`--files` などの指定は PATH より前に書く

### ANCHOR（付け先）

| 付け先 | 書き方 |
|---|---|
| 全体 | `review` |
| セクション | `section:<鍵>`（鍵は名前を ` / ` でつないだ道筋） |
| ファイル | `file:<パス>` |
| hunk | `hunk:<ID>` |
| 行 | `line:<hunk の ID>:<old か new>:<始め>-<終わり>`（hunk の中の 0 から数えた位置） |

### KIND（種類）

`instruction`（指示）・`question`（質問）・`reconsider`（再考）・`other`（その他）

### `wait` の終了コード

| 終了コード | 意味 |
|---|---|
| 0 | メッセージが届いた。出力に、答えるための文が出る |
| 3 | 時間切れ（25 分）。渡すものは無い |
| 4 | サーバーが動いていない（画面が閉じられた） |
