.PHONY: fmt test test-race test-preview audit gate test-db preview docker-build telegram-check telegram-smoke

fmt:
	go fmt ./...

test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

test-preview:
	node --test scripts/radar-lite-preview.test.mjs

audit:
	go run ./cmd/radar-lite audit

gate: audit test-race test-preview
	go vet ./...

test-db:
	test -n "$${RADAR_TEST_DATABASE_URL}" || (echo "RADAR_TEST_DATABASE_URL is required" >&2; exit 2)
	go test -race ./internal/lite ./cmd/radar-lite -count=1

preview:
	node scripts/radar-lite-preview.mjs

docker-build:
	docker build -t radar-lite:local .

telegram-check:
	test -f .env || (echo ".env is required" >&2; exit 2)
	set -a; . ./.env; set +a; GOCACHE="$${GOCACHE:-/tmp/radar-go-cache}" go run ./cmd/radar-telegram-smoke

telegram-smoke:
	test -f .env || (echo ".env is required" >&2; exit 2)
	set -a; . ./.env; set +a; GOCACHE="$${GOCACHE:-/tmp/radar-go-cache}" RADAR_LITE_PUBLISHING_ENABLED=true go run ./cmd/radar-telegram-smoke --send --confirm-channel "$${RADAR_LITE_TELEGRAM_CHAT_ID}"
