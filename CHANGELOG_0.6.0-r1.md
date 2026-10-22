# DDS Installer 0.6.0-r1 — Interrupted Companion Recovery

Date: 2026-10-09. Previous immutable candidate: 0.5.0-r1.

## Bounded change

- A synced `transaction.pending.json` journal is created in the version-local backup directory before installation bootstrap replacement.
- Journal records previous/new version identities and per-file SHA-256 for DDS.exe, DDSUpdater.exe and current.json.
- Successful Apply seals the record as `transaction.completed.json`; successful ordinary rollback seals it as `transaction.recovered.json`.
- An unfinished journal prevents another Apply, including the case when current.json already points to the new version.
- New explicit menu option `6` shows pending recovery, verifies previous backup bytes and live file states before mutation and requires exact Russian confirmation `ВОССТАНОВИТЬ`.
- Recovery restores previous bootstrap only; previous versioned payload, installed DataRoot, SQLite, plugin, Discord, BetterDiscord, registry and autostart are outside scope.
- If automated installed-root detection is unavailable, the user may explicitly provide an absolute DDS root; path and pending transaction are validated and an evident Portable profile is blocked.
- Repeated interruption during recovery is recoverable: journal remains pending until full verification succeeds.

## No scope expansion

- No automatic repair, no automatic execution of DDS, no backup pruning, no version directory deletion, no 0.5.0-r1 historical change, and no publication.
- A kill/interruption is simulated with verified staged/root file states; real power-loss/power-cut durability is NOT demonstrated. Directory metadata fsync on Windows is NOT proven.
- Installed Windows DDS remains untouched. No live Apply/rollback or version upgrade tested by this sandbox gate.
