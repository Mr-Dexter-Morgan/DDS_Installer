# DDS Installer 0.7.1-r1 — sandbox validation

**Candidate status:** SANDBOX PASS / WINDOWS X64 CROSS-BUILD PASS / WINDOWS ISOLATED LIVE NOT RUN / NOT RELEASED.

**Original baseline:** `DDS-Installer-0.7.0-r1-source.zip` (preserved unchanged). Existing Plugin Apply code already provided installation, SHA-256 validation, typed confirmation and backup; this patch fixes a rollback failure mode.

**Tests performed in sandbox (2026-10-10):**
- Targeted `go test -count=1 -run TestPlugin -v ./...`: PASS.
- Full `go test -count=1 ./...`: PASS; 98 top-level `Test...` functions in this candidate. Count includes non-Plugin tests.
- `go test -race -count=1 ./...`: PASS.
- `go vet ./...`: PASS.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build`: PASS, native x64 PE output verified by `file`.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c`: PASS, native x64 PE test output verified by `file`.

**Limits:** No Windows execution of this candidate yet. Synthetic temp directories do not prove Windows locking/rename behavior, real BetterDiscord loading, changes to production Plugin, or full Setup/Repair. Test Runner is isolated, not a production-apply authorization.
