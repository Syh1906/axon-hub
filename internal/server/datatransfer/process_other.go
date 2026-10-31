//go:build !linux && !windows

package datatransfer

import "os/exec"

func runCommand(cmd *exec.Cmd) error { return cmd.Run() }
