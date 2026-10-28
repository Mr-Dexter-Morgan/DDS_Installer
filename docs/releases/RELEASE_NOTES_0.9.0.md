# DDS Installer v0.9.0 — Stable release notes (prepared from GUI r7)

**Publication status:** prepared for stable release; this document alone does not mean a GitHub Release has been published.

Build: `0.9.0-gui-r7` — the exact Windows x64 executable accepted by the user on 2026-10-28.

## Verified installation behavior
- The 3-stage Windows setup wizard successfully completed with Discord, BetterDiscord, DDS Plugin and DDS Companion files reported installed.
- User confirmed Discord was closed during maintenance and launched afterward.
- User confirmed the Discord and DDS desktop shortcuts appeared.
- Linux unit tests/race detector/vet and Windows x64 cross-build passed; a rebuilt EXE matched the delivered GUI r7 EXE byte-for-byte.

## Changes relative to the public rc.1 (GUI r4)
- r5: verify current upstream Stable metadata before reusing staged packages; if the version or published SHA-256 differs, reacquire instead of reusing an old cache.
- r6: ownership-aware migration of Installer-managed `DDS.lnk` shortcuts.
- r7: independent Windows Discord process probe, diagnostic exit details for taskkill, bounded close/recheck intervals, and regression tests for the stop sequence.

## Known scope and limitations
- Confirmed by user for the observed Setup path; this is **not** a claim that every Windows installation scenario is tested.
- Fresh DDS Plugin heartbeat and the entire archive/capture runtime were not independently inspected in this chat; installed files alone are not functional proof.
- Standalone full Repair DDS, Uninstall, Portable and full clean-install coverage are not part of the tested stable Setup scope.
- No Authenticode signature claim.
- Existing user data and unrelated BetterDiscord plugins/themes must remain intact.

## Release assets from exact accepted GUI r7 (SHA-256)

```
839f68aafc94beface43adcc20f722d7f76c76e457d7643d1b9b7d23f7fb53a8  DDSSetup.exe
8d228110131bdf6eb78e15c991dcaccb49603a48812e9c1deaa497d87b1e915b  DDS-Installer-0.9.0-windows-x64-gui-r7.zip
71f8af920d972339af516eb8560b595ec38a808129bb7fb23bc7c50a927ac74d  DDS-Installer-0.9.0-gui-r7-source.zip
```

**Asset identity caution:** The two archives above retain their originally verified r7 bytes; do not rezip or rename their contents and then reuse these SHA-256 values. `DDSSetup.exe` is the same executable as `DDSSetup_0.9.0-gui-r7.exe`, renamed for Release assets only.

Historical GitHub `v0.9.0-rc.1` stays immutable.
