//go:build windows

package proc

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Explorer's /select switch must receive `/select,"C:\path with spaces"` as one
// token with only the path quoted; Go's default argv quoting would wrap the whole
// argument and Explorer silently falls back to a default folder.
func openPath(path string, isDir bool) error {
	cmd := exec.Command("explorer.exe")
	cmdLine := fmt.Sprintf(`explorer.exe "%s"`, path)
	if !isDir {
		cmdLine = fmt.Sprintf(`explorer.exe /select,"%s"`, path)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdLine}
	return cmd.Start()
}
