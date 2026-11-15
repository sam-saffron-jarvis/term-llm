package scripts

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProxyReleaseContextChecksumsAndStaticContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release preparation uses POSIX shell tools")
	}
	for _, mode := range []string{"valid", "bad-checksum", "dynamic"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			fixtures := filepath.Join(dir, "fixtures")
			bin := filepath.Join(dir, "bin")
			os.Mkdir(fixtures, 0700)
			os.Mkdir(bin, 0700)
			checksums := ""
			for _, arch := range []string{"amd64", "arm64"} {
				name := "term-llm_1.2.3_linux_" + arch + ".tar.gz"
				path := filepath.Join(fixtures, name)
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				gz := gzip.NewWriter(f)
				tarw := tar.NewWriter(gz)
				body := []byte("synthetic binary " + arch)
				tarw.WriteHeader(&tar.Header{Name: "term-llm", Mode: 0555, Size: int64(len(body))})
				tarw.Write(body)
				tarw.Close()
				gz.Close()
				f.Close()
				data, _ := os.ReadFile(path)
				hash := fmt.Sprintf("%x", sha256.Sum256(data))
				if mode == "bad-checksum" {
					hash = strings.Repeat("0", 64)
				}
				checksums += hash + "  " + name + "\n"
			}
			os.WriteFile(filepath.Join(fixtures, "checksums.txt"), []byte(checksums), 0600)
			writeExecutable(t, filepath.Join(bin, "gh"), `#!/bin/bash
set -eu
while [ "$#" -gt 0 ]; do
 if [ "$1" = --dir ]; then shift; dest=$1; fi
 shift
done
cp "$FIXTURES"/* "$dest/"
`)
			writeExecutable(t, filepath.Join(bin, "go"), "#!/bin/bash\nprintf 'build CGO_ENABLED="+map[bool]string{true: "1", false: "0"}[mode == "dynamic"]+"\\n'\n")
			output := filepath.Join(dir, "context")
			cmd := exec.Command("bash", "prepare-proxy-image.sh", "v1.2.3", output)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURES="+fixtures)
			data, err := cmd.CombinedOutput()
			if mode != "valid" {
				if err == nil {
					t.Fatal("unsafe release accepted", string(data))
				}
				return
			}
			if err != nil {
				t.Fatalf("prepare failed %v %s", err, data)
			}
			for _, file := range []string{"Dockerfile", "linux_amd64/term-llm", "linux_arm64/term-llm"} {
				if _, err := os.Stat(filepath.Join(output, file)); err != nil {
					t.Fatal(err)
				}
			}
			if !strings.Contains(string(data), "no image was built or published") {
				t.Fatal(string(data))
			}
		})
	}
}
