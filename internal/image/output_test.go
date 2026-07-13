package image

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestSaveImageConcurrentCallsDoNotOverwrite(t *testing.T) {
	const saveCount = 32

	outputDir := t.TempDir()
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
			paths[i], errs[i] = SaveImage(payloads[i], outputDir, "the same prompt")
		}(i)
	}

	close(start)
	wg.Wait()

	seen := make(map[string]int, saveCount)
	for i := 0; i < saveCount; i++ {
		if errs[i] != nil {
			t.Fatalf("SaveImage call %d failed: %v", i, errs[i])
		}
		if previous, exists := seen[paths[i]]; exists {
			t.Fatalf("SaveImage calls %d and %d returned the same path %q", previous, i, paths[i])
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
