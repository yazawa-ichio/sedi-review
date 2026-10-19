// Package gitx は git のコマンドを呼ぶ。オプションは足さず、ユーザーの設定に従う。
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Run は dir で git を実行し、標準出力を返す。
func Run(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	hideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// TopLevel はリポジトリの最上位のパスを返す。
func TopLevel(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveCommit は ref をコミットのハッシュに解決する。
func ResolveCommit(dir, ref string) (string, error) {
	out, err := Run(dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Config は設定の値を返す。無ければ空文字。
func Config(dir, key string) string {
	out, err := Run(dir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Diff は base と target の差分を返す。threeDot なら A...B、そうでなければ A B。
func Diff(dir, base, target string, threeDot bool, pathspecs []string) ([]byte, error) {
	args := []string{"diff"}
	if threeDot {
		args = append(args, base+"..."+target)
	} else {
		args = append(args, base, target)
	}
	args = append(args, "--")
	args = append(args, pathspecs...)
	return Run(dir, args...)
}

// Blob は blob の中身を返す。
func Blob(dir, id string) ([]byte, error) {
	return Run(dir, "cat-file", "blob", id)
}
