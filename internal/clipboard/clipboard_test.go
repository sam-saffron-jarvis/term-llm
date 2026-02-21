package clipboard

import "testing"

func TestDetectPreferredImageMIME(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "prefers png when available",
			in:   "text/plain\nimage/jpeg\nimage/png\n",
			want: "image/png",
		},
		{
			name: "accepts jpeg when png absent",
			in:   "text/plain\nimage/jpg\n",
			want: "image/jpeg",
		},
		{
			name: "accepts first image type as fallback",
			in:   "text/plain\nimage/bmp\napplication/json\n",
			want: "image/bmp",
		},
		{
			name: "ignores non image types",
			in:   "text/plain\napplication/octet-stream\n",
			want: "",
		},
		{
			name: "strips mime parameters",
			in:   "image/webp; charset=binary\n",
			want: "image/webp",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectPreferredImageMIME(tc.in)
			if got != tc.want {
				t.Fatalf("detectPreferredImageMIME() = %q, want %q", got, tc.want)
			}
		})
	}
}
