package pipeline

import "strings"

func CompactDiscoveryError(message string) string {
	message = strings.Join(strings.Fields(strings.ToValidUTF8(message, "")), " ")
	return TruncateText(message, 1000)
}

// TruncateText returns a valid UTF-8 prefix no larger than maxBytes. External
// providers can put arbitrary text in errors and evidence; preserving the
// database write is more important than retaining a partial final rune.
func TruncateText(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, "")
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && (value[end]&0xc0) == 0x80 {
		end--
	}
	return value[:end]
}
