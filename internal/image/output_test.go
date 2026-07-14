package image

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSaveImageFileConcurrentCallsDoNotOverwrite(t *testing.T) {
	const saveCount = 32

	outputDir := t.TempDir()
	filename := "fixed-image.png"
	originalPath := filepath.Join(outputDir, filename)
	originalData := []byte("existing image")
	if err := os.WriteFile(originalPath, originalData, 0644); err != nil {
		t.Fatalf("write existing image: %v", err)
	}

	paths := make([]string, saveCount)
	payloads := make([][]byte, saveCount)
	errs := make([]error, saveCount)
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(saveCount)
	for i := 0; i < saveCount; i++ {
		payloads[i] = []byte(fmt.Sprintf("image-%d", i))
		go func(i int) {
			defer wg.Done()
			<-start
			paths[i], errs[i] = saveImageFile(payloads[i], outputDir, filename)
		}(i)
	}

	close(start)
	wg.Wait()

	gotOriginal, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read existing image: %v", err)
	}
	if !bytes.Equal(gotOriginal, originalData) {
		t.Fatalf("existing image contains %q, want %q", gotOriginal, originalData)
	}

	seen := make(map[string]int, saveCount)
	for i := 0; i < saveCount; i++ {
		if errs[i] != nil {
			t.Fatalf("saveImageFile call %d failed: %v", i, errs[i])
		}
		if paths[i] == originalPath {
			t.Fatalf("saveImageFile call %d overwrote existing path %q", i, paths[i])
		}
		if previous, exists := seen[paths[i]]; exists {
			t.Fatalf("saveImageFile calls %d and %d returned the same path %q", previous, i, paths[i])
		}
		seen[paths[i]] = i

		got, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatalf("read image from call %d: %v", i, err)
		}
		if !bytes.Equal(got, payloads[i]) {
			t.Fatalf("image from call %d contains %q, want %q", i, got, payloads[i])
		}
	}
}
