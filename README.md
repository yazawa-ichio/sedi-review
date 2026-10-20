# Sedi Review

AI が書いた大きな差分を、意味のセクションに分けて読むためのローカルの道具。

- [仕組み](docs/design.md)
- [命令の一覧](docs/cli.md)
- [エージェント向けの skill](skills/sedi-review/SKILL.md)

## 入れる

Go 1.24 以上で:

```bash
go install github.com/yazawa-ichio/sedi-review@latest
```

- `sedi-review` という名前の実行ファイルが `$GOBIN` に入る。`GOBIN` を設定していなければ `$(go env GOPATH)/bin`
  （既定では Windows が `%USERPROFILE%\go\bin`、macOS と Linux が `~/go/bin`）
- そのフォルダが PATH に通っていなければ、PATH に足す
- 版を固定するときは `@latest` の代わりに `@v0.1.0` のように書く。入っている版は `sedi-review version` で分かる
- 画面のファイルとエージェント向けの skill は実行ファイルに埋め込まれているので、入れるのはこの 1 つだけでよい
- 新しくするときは、同じ命令をもう一度実行する。skill も入れているなら、そのあと `sedi-review skill status` で確かめ、
  違っていれば `sedi-review skill install --agent <入れた先> --force` で入れ直す

以下の例は、`sedi-review` が PATH の通った場所にある前提で書いている。

## 使う

レビューしたいリポジトリで、最初に 1 回だけ初期化する:

```bash
sedi-review init --base main
```

- `.sedi-review/` を作る。レビューの状態（セクション・確認済み・メッセージ・書き出し）はここに入る。
  中の `.gitignore` が自分を git から外すので、リポジトリの `.gitignore` は書き換えない
- `.sedi-review.json`（設定）を書く。既定の比べる元・比べ方・ルールを持つ。共有するならコミットする
- 掃除は `.sedi-review/` を消すだけ

開く:

```bash
sedi-review open --target work --open
```

- 比べる元は設定の `base`（`--base` で変えられる）。既定は `main...work`（分かれた点から work まで）。`--two-dot` で `main work`
- 対象を絞るときは `--` の後に git の pathspec を書く。例: コードだけなら `-- '*.cs'`、フォルダなら `-- Assets/App`。
  ほかの例は [命令の一覧](docs/cli.md#開く指定の例)
- 名前を付けたルールは `.sedi-review.json` の `rules` に書き、`--rule` で選ぶ。組み込みは `unity`（`*.cs.meta` を外す）と `all`
- 画面のタブを閉じて 30 秒たつと終わる。同じ指定で開き直すと、前の続きから読める

## エージェントとのやり取り

1. 画面の「全体」→「セクションを頼む」でメッセージを書き、「コピー」してエージェントのチャットに貼る
2. エージェントは貼られた文の手順で `hunks` を見て、`sections set` でセクションを渡す
3. 行・hunk・ファイル・セクション・全体にメッセージを書き、個別の「コピー」か「一括コピー」で貼る
4. エージェントはチャットで答え、同じ内容を `reply` で道具にも書く。画面に出る

エージェント向けの命令は、渡す文に `--repo` と `--review` が入っているので、どこから実行しても同じレビューに届く。

待ち受け（任意）: エージェントが `sedi-review wait --repo <リポジトリ> --review <ID>` を裏で動かしている間は、
画面に「送る」「まとめて送る」が出て、貼らずに渡せる。

### エージェントに skill を入れる

エージェントが受け持つ作業（セクションを作る、メッセージに答える、待ち受け）の手順は、
[`skills/sedi-review/SKILL.md`](skills/sedi-review/SKILL.md) にある。[Agent Skills](https://agentskills.io) の形式なので、
Claude Code と Codex のどちらでも使える。

skill は実行ファイルに埋め込んであり、実行ファイルと同じ版の手順を入れられる:

```bash
sedi-review skill install --agent claude            # Claude Code に入れる
sedi-review skill install --agent claude,codex      # 両方に入れる
sedi-review skill install --agent codex --scope project   # このリポジトリだけで使う
sedi-review skill status                            # どこに入っていて、この版と同じか
sedi-review skill uninstall --agent claude
```

- 入れる先は `--agent` で選ぶ。指定しないと何もしない
- 手で直した skill や古い版が入っていれば、置き換えずに止まる。置き換えるときは `--force` を付ける
- `sedi-review` を新しくしたら、`skill status` で確かめ、`skill install --force` で入れ直す
- 消すときは skill のファイルだけを消す。利用者が足したファイルは残す

入れる先:

| エージェント | `--scope user`（既定。すべてのリポジトリ） | `--scope project`（このリポジトリだけ） |
|---|---|---|
| Claude Code | `~/.claude/skills/sedi-review/`（`CLAUDE_CONFIG_DIR` があればその下） | `<リポジトリ>/.claude/skills/sedi-review/` |
| Codex | `~/.agents/skills/sedi-review/` | `<リポジトリ>/.agents/skills/sedi-review/` |

実行ファイルを使わずに、`skills/sedi-review/` のフォルダを上の場所へ手で写してもよい。

## ライセンス

コードは [MIT License](LICENSE) です。このライセンスが対象とするのはコードだけで、
Sedi Review を使って作ったもの（セクション、確認済み、メッセージ、書き出した文など）は対象にしません。

同梱している第三者のソフトウェア:

- [Prism](https://prismjs.com) 1.30.0（MIT License）: `web/static/vendor/prism.js`、許諾は `web/static/vendor/PRISM-LICENSE.txt`
- Go の標準ライブラリ（BSD 3-Clause License）: 実行ファイルに含まれる。許諾は `web/static/vendor/GO-LICENSE.txt`

画面の「ヘルプ」でも、これらの許諾の全文を見られる。

## 開発

このリポジトリの直下で作る:

```bash
go build -o sedi-review.exe .
```

macOS 向け: `GOOS=darwin GOARCH=arm64 go build -o sedi-review .`

画面のファイルは実行ファイルに埋め込まれる。直しながら見るときは、環境変数 `SEDI_REVIEW_WEB` にこのリポジトリの
`web/static` を指して、ディスクから読ませる（レビューするリポジトリの中で実行する）:

```bash
SEDI_REVIEW_WEB=<このリポジトリ>/web/static sedi-review open --base main --port 4791
```

```bash
go vet ./... && go test ./...
```
