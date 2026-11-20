//go:build !windows

package fssecure

import (
	"io/fs"
	"os"
)

// restrict applies the POSIX mode directly. | restrict 直接施加 POSIX 模式。
func restrict(path string, mode fs.FileMode) error {
	return os.Chmod(path, mode.Perm())
}
