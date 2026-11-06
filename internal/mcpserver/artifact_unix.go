//go:build !windows

package mcpserver

import (
	"os"
	"syscall"
)

func openArtifact(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
