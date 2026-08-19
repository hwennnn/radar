package delivery

import (
	"strings"
	"time"
)

// MaxDeliveryRetryDelay caps provider Retry-After hints before persistence.
const MaxDeliveryRetryDelay = time.Hour

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
