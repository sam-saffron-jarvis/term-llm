//go:build unix

package proxyconnect

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether the file belongs to the invoking user.
func ownedByCurrentUser(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}
