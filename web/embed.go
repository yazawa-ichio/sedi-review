// Package web は画面のファイルを実行ファイルに埋め込む。
package web

import "embed"

//go:embed static
var Files embed.FS
