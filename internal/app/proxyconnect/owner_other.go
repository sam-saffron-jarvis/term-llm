//go:build !unix

package proxyconnect

import "io/fs"

// ownedByCurrentUser is a no-op where POSIX ownership does not apply; the
// user profile's ACL is the boundary there.
func ownedByCurrentUser(fs.FileInfo) bool { return true }
