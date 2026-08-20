# Radar Command Instructions

This directory is the process entrypoint only. Keep `main.go` limited to signal
handling, logger construction, argument forwarding, and exit behavior. Runtime
modes, configuration, HTTP, and pipeline wiring belong in `internal/app`.
