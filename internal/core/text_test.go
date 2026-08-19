package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateTextPreservesValidUTF8(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("界", 400)
	got := truncateText(input, 1000)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateText returned invalid UTF-8: %q", got)
	}
	if len(got) > 1000 {
		t.Fatalf("truncateText returned %d bytes, want at most 1000", len(got))
	}
	if got != strings.Repeat("界", 333) {
		t.Fatalf("truncateText split an unexpected prefix: got %d runes", utf8.RuneCountInString(got))
	}
}

func TestTruncateTextSanitizesInvalidInput(t *testing.T) {
	t.Parallel()

	got := truncateText("ok\xffstill", 100)
	if got != "okstill" {
		t.Fatalf("truncateText = %q, want %q", got, "okstill")
	}
}

func TestTruncateTextHonorsNonPositiveLimit(t *testing.T) {
	t.Parallel()

	if got := truncateText("value", 0); got != "" {
		t.Fatalf("truncateText = %q, want empty", got)
	}
}
