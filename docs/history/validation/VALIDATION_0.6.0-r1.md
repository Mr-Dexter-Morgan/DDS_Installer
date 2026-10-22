# DDS Installer 0.6.0-r1 — Sandbox validation

**Status: AUTOMATED PASS / Windows LIVE PENDING / NOT RELEASED.**

- Baseline 0.5.0-r1 copied as immutable upstream input; feature changes only in new 0.6.0-r1 copy.
- Go tests: 88 top-level `Test...` functions PASS (`go test ./...`), including synthetic transaction interruption after 0/1/2/3 root bootstrap replacements, journal persistence, recovery re-interruption/resume, corrupted journal, damaged backup, independently modified root, pending transaction blocking new Apply, missing detection fallback, Portable boundary, confirmation, success/failure journal completion.
- Race detector: `go test -race ./...` PASS.
- Static diagnostics: `go vet ./...` PASS.
- Windows/amd64 `DDSSetup` PE32+ cross-compilation PASS.
- Windows/amd64 test binary PE32+ cross-compilation PASS.
- Exact output hashes recorded in `BUILD_REPORT_0.6.0-r1.json` and sidecars.

### Validation limits

- Tests simulate interrupted states using isolated temporary directories, not a process termination or physical power loss.
- Native Windows interrupted-install/recovery tests are still pending. Non-atomic storage hardware failure, directory journal durability, antivirus file-lock behavior, and actual installed DataRoot diff are unverified.
- A preliminary validation run failed due to a 4-vs-3 bootstrap hash subset mismatch; corrected and full prior suite passed on retry. A later input-reader issue on explicit-root manual recovery was also detected and corrected; subsequent full suite passed.
- Final combined validation process printed completed PASS lines before ancillary test-count shell pipeline timed out; test count was subsequently obtained directly with Python, not claimed as part of that pipeline.
