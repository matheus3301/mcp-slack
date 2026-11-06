//go:build windows

package mcpserver

import "os"

func openArtifact(_ *os.Root, _ string) (*os.File, error) { return nil, denied() }
