# DDS Installer 0.8.0-r1 — Windows LIVE gate (PENDING)

**Target only disposable Windows VM/clean test profile** with no valuable installed DDS. Do NOT test external Discord or bdcli installation against the user's working DDS profile.

- Verify exact candidate ZIP SHA-256 and PE32+ Windows x64 binary identity before launching.
- Clean-machine: missing Discord -> official signed DiscordSetup.exe -> real re-detect -> verified bdcli stable installation -> real BetterDiscord re-detect -> DDS Plugin 0.5.3 post-hash/version -> DDS Companion 0.8.5 file placement/post-detect. Confirm each actual side effect; avoid claiming full pass based on simulation.
- Verify two confirmations; `Enter`/`NO` means no mutating Apply.
- Trigger missing/corrupted stage, cancel external Discord installer, failed BetterDiscord CLI, running Discord at Plugin Apply, insufficient space, and re-run after interruption. Confirm dependencies stop without false success and old files remain protected.
- Re-run with fully installed healthy chain: report no modifications, no downloads.
- Real Companion first GUI start / ACK, DataRoot archive SQLite quick_check/diff, Plugin heartbeat, DDS_Data link, Portable and Repair are later acceptance gates and NOT covered by this candidate's synthetic regression.
- Record process exit code, output, exact tested SHA-256 and observed data changes. **Only mark LIVE PASS after actual Windows execution.**
