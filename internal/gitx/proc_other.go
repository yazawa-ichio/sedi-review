//go:build !windows

package gitx

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}

// HideWindow は Windows 以外では何もしない。
func HideWindow(cmd *exec.Cmd) { hideWindow(cmd) }
