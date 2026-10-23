# DDS Installer v0.9.0-rc.1

Status: **PUBLIC PRE-RELEASE / PREVIEW**  
Source baseline: **0.9.0-gui-r4**  
Published: **2026-10-22**

## Scope

This preview fixes the setup path where BetterDiscord/DDS Plugin could appear present from files while the runtime integration still required repair.

## Changes

- DDS setup requires the BetterDiscord operation when selected Plugin/Companion integration depends on it.
- Re-runs the intended BetterDiscord integration path after closing Discord when required.
- Validates BetterDiscord loader state after apply.
- Validates and enables DDS Plugin without resetting unrelated plugin/theme state.
- Tightens post-Finish runtime checks so stale/mismatched heartbeat data is not accepted as Plugin success.
- Preserves the Program Files / user DataRoot separation.
- Keeps explicit Finish-page launch choices.
- Adds Windows GitHub Actions test/vet/native-build verification.

## Public status

This is not yet a Stable Installer release.

Not claimed as fully accepted in this preview:

- standalone full Repair;
- Uninstall;
- Portable setup;
- complete clean-install acceptance across all supported Windows states.

Installer, Plugin and Companion versions are independent.

Release page:

https://github.com/Mr-Dexter-Morgan/DDS_Installer/releases/tag/v0.9.0-rc.1
