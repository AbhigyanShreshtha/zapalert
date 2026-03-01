# Changelog

All notable changes to this project will be documented in this file.

## [0.1.0] - 2026-03-01

### Added

- Initial `zapalert` library with structured JSON logging on top of Uber zap.
- Public `zapalert.Logger` API with `Debug`, `Info`, `Warn`, `Error`, `Alert`, `ObserveRequest`, and `Sync`.
- Escalation engine (`alert` package) with:
  - rolling-window count thresholds
  - rolling-window failure-rate thresholds
  - configurable ladder and rule matching (regex)
  - cooldown and de-escalation controls
- Backend abstraction with implementations:
  - in-memory sliding-window backend (`backend/inmem`)
  - Redis backend (`backend/redis`) using bucketed keys and pipelined snapshots
- Context metadata package (`ctxmeta`) with request/client/IP/user-agent helpers.
- Logger builder package (`logger`) using `zapcore.NewCore` and JSON encoder defaults.
- Runnable examples:
  - `examples/basic`
  - `examples/http_middleware`
  - `examples/redis`
- Unit/integration tests for escalation logic, in-memory rotation, and Redis snapshot/keying.
