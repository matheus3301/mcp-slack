//go:build windows

package receipts

import "os"

func owned(_ os.FileInfo, _ bool) bool { return false }
