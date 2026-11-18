//go:build linux && amd64 && rustproxy

package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// The prototype build target generates this from the stripped Rust executable.
//
//go:embed proxy_rust_payload.gz
var rustProxyPayload []byte

func init() { dispatchProxy = execRustProxy }

func execRustProxy(args []string) error {
	if len(args) < 2 || args[0] != "serve" || args[1] != "proxy" {
		return nil
	}
	payload, err := gzip.NewReader(bytes.NewReader(rustProxyPayload))
	if err != nil {
		return err
	}
	defer payload.Close()

	fd, err := unix.MemfdCreate("term-llm-rust-proxy", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "term-llm-rust-proxy")
	defer file.Close()
	if _, err := io.Copy(file, payload); err != nil {
		return err
	}
	if err := file.Chmod(0500); err != nil {
		return err
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_SEAL); err != nil {
		return err
	}
	argv := append([]string{"term-llm serve proxy"}, args[2:]...)
	return unix.Exec("/proc/self/fd/"+strconv.Itoa(fd), argv, os.Environ())
}
