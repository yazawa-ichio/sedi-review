package gitx

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// HideWindow は他の子プロセスにも同じ指定をする。
func HideWindow(cmd *exec.Cmd) { hideWindow(cmd) }
