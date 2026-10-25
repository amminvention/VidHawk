package naming

import "testing"

func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, page, want string }{
		{"Dar-e-nijaat - Detail Page", "https://aryplus.tv/series/abc", "Dar-e-nijaat"},
		{"(3) Some Video Title - YouTube", "https://www.youtube.com/watch?v=x", "Some Video Title"},
		{"Watch Online | Episode 5 - Great Show", "https://example.com/x", "Episode 5 - Great Show"},
		{"Plain Title", "https://example.com", "Plain Title"},
		{"Example - Big Buck Bunny", "https://example.com", "Big Buck Bunny"},
	}
	for _, c := range cases {
		if got := CleanTitle(c.in, c.page); got != c.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPickTitlePrefersStructuredMetadata(t *testing.T) {
	m := Meta{OG: "Dar-E-Nijaat", Title: "Dar-e-nijaat - Detail Page"}
	if got := PickTitle(m, "https://aryplus.tv/series/abc"); got != "Dar-E-Nijaat" {
		t.Fatalf("got %q", got)
	}
	if got := PickTitle(Meta{Title: "Detail Page"}, "https://aryplus.tv/"); got != "" {
		t.Fatalf("generic title should be rejected, got %q", got)
	}
}

func TestSanitize(t *testing.T) {
	if got := Sanitize(`a<b>:"c/d\e|f?g*h.mp4`); got != "abcdefgh.mp4" {
		t.Fatalf("got %q", got)
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	got := Sanitize(string(long) + ".mp4")
	if len(got) > 150 || got[len(got)-4:] != ".mp4" {
		t.Fatalf("long name not capped correctly: %d %q", len(got), got[len(got)-8:])
	}
}

func TestContentDisposition(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="report.pdf"`:                  "report.pdf",
		`attachment; filename*=UTF-8''na%C3%AFve%20file.zip`: "naïve file.zip",
		`inline; filename=plain.mp4`:                         "plain.mp4",
		`attachment; filename="..\\evil.exe"`:                ".._evil.exe",
		``:                                                   "",
	}
	for h, want := range cases {
		if got := ContentDispositionFilename(h); got != want {
			t.Errorf("%q → %q, want %q", h, got, want)
		}
	}
}

func TestFileName(t *testing.T) {
	cases := []struct{ title, cd, url, ct, want string }{
		{"", "", "https://x.com/a/b/video.mp4?sig=1", "video/mp4", "video.mp4"},
		{"My Clip", "", "https://x.com/stream?id=9", "video/mp4", "My Clip.mp4"},
		{"My Clip.mkv", "", "https://x.com/stream", "", "My Clip.mkv"},
		{"", "named.zip", "https://x.com/dl", "application/octet-stream", "named.zip"},
		{"", "", "https://x.com/get.php", "application/pdf", "get.pdf"},
	}
	for _, c := range cases {
		if got := FileName(c.title, c.cd, c.url, c.ct); got != c.want {
			t.Errorf("FileName(%q,%q,%q,%q) = %q, want %q", c.title, c.cd, c.url, c.ct, got, c.want)
		}
	}
	if got := MediaName("Show", "mp4", "1080p"); got != "Show [1080p].mp4" {
		t.Fatalf("got %q", got)
	}
}
