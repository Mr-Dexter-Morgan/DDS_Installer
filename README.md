# DDS Installer

Windows GUI installer for **DDS — Discord Data Snatcher**, a personal local-first Discord archive companion.

## Current pre-release

**v0.9.0-rc.1** is based on the accepted **0.9.0-gui-r4** candidate. The targeted Windows live test on 21 October 2026 confirmed the BetterDiscord reinjection and DDS Plugin startup fix.

Download binaries from **Releases** and verify them with the published SHA-256 checksums.

## What it does

- Russian three-step GUI wizard: Settings → Install → Finish.
- Detects Discord Stable, BetterDiscord, DDS Plugin, and DDS Companion.
- Re-runs the official BetterDiscord CLI during an explicitly started DDS setup even when BetterDiscord files are already detected.
- Validates and enables only DDS Plugin while preserving other plugins and themes.
- Installs or preserves DDS Companion without touching the user's archive, SQLite database, or settings.
- Uses `Program Files\\DDS` for a new program installation and keeps user data under `%LOCALAPPDATA%\\DDS_Companion`.
- Supports an optional desktop shortcut and post-exit launch of selected applications.

## Status and limitations

This is a **pre-release**, not Stable.

The r4 BetterDiscord / DDS Plugin repair path passed its targeted Windows live test. Full standalone DDS Repair, Uninstall, Portable setup, and a separate fresh-install Program Files live gate are not yet accepted.

Installer, DDS Plugin, and DDS Companion have independent versions. The current release candidate is not claimed to be Authenticode-signed.

## Build

Requirements:

- Windows x64
- Go 1.23+

Example build:

```powershell
go build -trimpath -ldflags="-H windowsgui -s -w" -o DDSSetup.exe .
```

The repository was populated from the immutable SHA-256-verified 0.9.0-gui-r4 source archive.

Source ZIP SHA-256:

```text
4afa367bc69bb4dcccb45c7484763435a1a23b196df327c74ceee8e40577aad1
```

## Repository layout

- `.github/` — CI workflow.
- `gui_assets/` — GUI assets.
- `*.go`, `*_test.go` — installer source and tests.
- `docs/` — project documentation and archived development evidence.
- `CHANGELOG.md` — concise public changelog.

Historical per-revision reports are preserved under `docs/history/`; they are kept for traceability and are not the primary user documentation.

No redistribution license is implied by publicly viewing this repository.
