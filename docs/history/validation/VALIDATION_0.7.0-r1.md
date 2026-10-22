# DDS Installer 0.7.0-r1 — Sandbox Validation

- 94 Go test functions (88 previous + 6 first-install functions): `go test ./... -count=1` PASS.
- `go test -race ./... -count=1`: PASS.
- `go vet ./...`: PASS.
- Windows amd64 EXE and Windows Go test EXE cross-compile: PASS, PE32+ x64.
- Windows LIVE: NOT RUN. No Windows production installation or startup verification.
- Regression scope: existing detection/acquire/stage, plugin/Companion apply, recovery and crash-journal tests were included in full tests.
