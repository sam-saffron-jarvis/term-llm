package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequestImageData_LoadsBase64FromInlineImagePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	part := Part{
		Type:            PartImage,
		ImageData:       &ToolImageData{MediaType: "image/png"},
		ImagePath:       filepath.Join(dir, "original.png"),
		InlineImagePath: path,
	}

	mediaType, base64Data, ok := requestImageData(part)
	if !ok {
		t.Fatal("requestImageData() = not ok, want ok")
	}
	if mediaType != "image/png" {
		t.Fatalf("mediaType = %q, want image/png", mediaType)
	}
	if base64Data != "aGVsbG8=" {
		t.Fatalf("base64Data = %q, want aGVsbG8=", base64Data)
	}
}
