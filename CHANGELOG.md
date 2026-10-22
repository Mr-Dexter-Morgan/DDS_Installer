# Changelog

## v0.9.0-rc.1 — 2026-10-22

Public pre-release based on DDS Installer 0.9.0-gui-r4.

Highlights:

- Fixed the setup path that could incorrectly skip BetterDiscord reinjection when BetterDiscord files were already detected.
- DDS setup now requires the BetterDiscord operation when DDS Plugin or Companion installation depends on it.
- Re-runs the official BetterDiscord CLI after closing Discord and validates the installed loader files.
- Validates and enables DDS Plugin without changing unrelated plugin states.
- Tightened post-Finish runtime status checks so stale or mismatched heartbeat data is not accepted as a successful DDS Plugin start.
- Preserved the existing GUI, Program Files/UAC model, archive/data paths, and independent Finish-page launch options.
- Added Windows GitHub Actions build verification.

The published pre-release contains `DDSSetup.exe`, the candidate ZIP, the immutable source ZIP, and `SHA256SUMS.txt`.

Detailed historical changelogs for all development revisions are preserved in `docs/history/changelog/`.
