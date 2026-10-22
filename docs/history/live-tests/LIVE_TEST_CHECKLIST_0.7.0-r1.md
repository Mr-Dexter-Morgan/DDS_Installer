# Windows isolated test gate — DDS Installer 0.7.0-r1

Scope: only temporary synthetic `%LOCALAPPDATA%` under `testing.TempDir` on Aspire-V5, never the real installed DDS.

1. Verify exact candidate and test executable SHA-256.
2. Execute Windows Go test binary with `-test.run=^TestFreshCompanion` / `-test.v` / `-test.count=1`; check process exit code 0.
3. Expect copied new root `DDS` to match staged payload; no existing target overwritten, installed profile blocked, stale/tampered staging rejected.
4. Confirm denied input does not write and refused `beforeCommit` removes scratch; known synthetic DataRoot sentinel unchanged.
5. Delete temporary Downloads harness directory after PASS; preserve archive/evidence on project Drive.

No claim of real process startup/real DataRoot archive validation or physical power-off test.
