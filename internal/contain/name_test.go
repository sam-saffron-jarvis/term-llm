package contain

import "testing"

func TestValidateName(t *testing.T) {
	valid := []string{"foo", "ruby-app", "go_test", "v1.2"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{"", "/", "../x", ".", "..", "foo/bar", `foo\\bar`, "name with spaces"}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Fatalf("ValidateName(%q) succeeded, want error", name)
		}
	}
}

func TestProjectNameNormalization(t *testing.T) {
	got := ProjectName("Ruby.App_1")
	want := "term-llm-contain-ruby-app_1"
	if got != want {
		t.Fatalf("ProjectName() = %q, want %q", got, want)
	}
	if ProjectName("Ruby.App_1") != got {
		t.Fatal("ProjectName normalization is not deterministic")
	}
}
