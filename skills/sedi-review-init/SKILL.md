---
name: sedi-review-init
description: Sedi Review（差分を意味のセクションに分けて読むローカルのレビューの道具）を、リポジトリで使い始めるための準備の手順。sedi-review の CLI を入れる・新しくする、リポジトリで sedi-review init をして比べる元や対象の規則（コードだけ等）を設定する、エージェント向けの skill を入れる、と頼まれたときに使う。「Sedi Review を導入して」「このリポジトリで Sedi Review を使えるようにして」など。
license: MIT
---

# Sedi Review を使い始める準備

Sedi Review を、作業者（人）のリポジトリで使えるようにします。順に進め、**決めることは作業者に確かめてから**実行します。
ソフトウェアを入れること、コミットすること、比べる元、対象の規則、skill を入れる先は、作業者が決めます。

## 1. CLI があるか確かめる

```bash
sedi-review version
```

- 版が出れば入っています。古ければ、新しくするかを作業者に聞きます
- 見つからなければ、入れるかを作業者に聞いてから入れます。Go 1.24 以上が要ります

  ```bash
  go version
  go install github.com/yazawa-ichio/sedi-review@latest
  ```

  入るのは `$GOBIN`（設定が無ければ `$(go env GOPATH)/bin`）です。そこが PATH に無ければ、作業者に伝えます。
  版を固定するときは `@latest` の代わりに `@v0.1.0` のように書きます

## 2. リポジトリの今の状態を見る

リポジトリの直下で確かめます。

```bash
git rev-parse --show-toplevel
ls -a .sedi-review.json .sedi-review 2>/dev/null
```

- `.sedi-review.json` があれば、初期化は済んでいます。中身を見せ、変えたいところがあるかを聞きます。無ければ 3 へ
- `.sedi-review/` は、レビューの状態を置くフォルダです。中の `.gitignore` で自分を git から外しているので、リポジトリの `.gitignore` に足す必要はありません

## 3. 比べる元を決める

`open` の既定の比べる元（`base`）です。ふつうは既定のブランチです。候補を調べて、作業者に確かめます。

```bash
git symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null   # 例: origin/main
git branch --list main master
```

- 既定は `A...B`（分かれた点から作業ブランチまで）です。作業ブランチ側の変更だけが出ます。両方の先をそのまま比べたいなら `--two-dot` です
- 作業者が既定のブランチの上で直接コミットしている場合は、`base` を何にしても、ふだんの `open` では `--base` を指定することになります。そのことも伝えます

## 4. 対象の規則を決める

リポジトリの中身を見て、案を作り、作業者に確かめます。規則は git の pathspec の並びです。

| リポジトリ | 案 |
|---|---|
| Unity（`Assets/` と `ProjectSettings/` がある） | 既定の `unity`（`*.cs.meta` だけを外す）のまま。コードだけを読む規則 `code` も足す |
| それ以外 | 既定の `unity` は、`*.cs.meta` が無ければ何も外さないので、そのままでもよい。必要なら `all`（何も外さない）を既定にする |
| 生成したファイルや、取り込んだ第三者のコードがある | そのフォルダを外す規則を足す |

規則の例（言語に合わせて変えます）:

```json
{
  "rules": {
    "unity": [":(exclude,glob)**/*.cs.meta"],
    "code": [":(glob)**/*.cs"],
    "app": ["Assets/App", ":(exclude)Assets/App/Generated"]
  }
}
```

## 5. 初期化する

決めた内容で実行します。

```bash
sedi-review init --base <比べる元>            # 例: --base main
sedi-review init --base main --two-dot        # 既定の比べ方を A B にするとき
```

- `.sedi-review/` を作り、設定 `.sedi-review.json` を書きます
- 既に設定があれば書き直しません。書き直すときは `--force` です（作業者に確かめてから）
- `--rule` で選べるのは、組み込みの規則（`unity` か `all`）だけです
- 4 で足す規則は、`init` のあとで `.sedi-review.json` の `rules` に書き足します。足した規則を既定にするなら、同じファイルの `rule` をその名前にします。書いたら中身を作業者に見せます

  ```json
  { "base": "main", "rule": "code", "rules": { "unity": [":(exclude,glob)**/*.cs.meta"], "code": [":(glob)**/*.cs"] } }
  ```

設定 `.sedi-review.json` は、チームで共有するためのファイルです。**コミットするかは作業者が決めます。** リポジトリにコミットの決まりがあれば、それに従います。

## 6. エージェント向けの skill を入れる

入れる先を作業者に聞きます。

```bash
sedi-review skill install --agent claude                    # Claude Code に、すべてのリポジトリで使う形で
sedi-review skill install --agent claude,codex              # 両方に
sedi-review skill install --agent codex --scope project     # このリポジトリだけで使う形で
sedi-review skill status
```

- 入るのは、作業の手順の `sedi-review` と、この準備の手順の `sedi-review-init` です。片方だけなら `--skill sedi-review`
- `--scope user`（既定）は利用者のフォルダ（`~/.claude/skills/` か `~/.agents/skills/`）に、`--scope project` はリポジトリの中（`.claude/skills/` か `.agents/skills/`）に入ります。リポジトリの中に入れた場合、コミットするかは作業者が決めます
- 手で直した skill や古い版があれば、置き換えずに止まります。置き換えるかは作業者に聞き、よければ `--force` を付けます
- 新しく入れた skill は、エージェントの新しいセッションから使えます

## 7. 動くか確かめる

```bash
sedi-review list                              # まだレビューが無ければ何も出ない
sedi-review open --target <作業ブランチ>       # 画面の URL が出る。--open でブラウザも開く
```

- 画面のタブを閉じて 30 秒たつと、サーバーは終わります
- 1 つのコミットだけを見るなら `--base <コミット>~1 --target <コミット>`、コードだけなら `-- '*.cs'` のように足します

## 8. 作業者に伝える

最後に、したことをまとめて伝えます。

- 入れた CLI の版
- 比べる元、比べ方、既定の規則、足した規則
- できたファイル（`.sedi-review.json`、`.sedi-review/`）と、コミットしたかどうか
- 入れた skill とその場所
- 開き方の例
