# DDS Installer 0.8.0-r3 — Automatic Setup / First Start

2026-10-19. Scope: one bounded FIRST SETUP automation patch atop 0.8.0-r2.

- After user approves the overall Setup, close Discord automatically before BetterDiscord CLI and again before Plugin file installation. Attempt graceful exit first; bounded forced closure if necessary. Warn user before approval to save drafts/leave voice chat.
- Enable only the freshly installed DDS Plugin by setting `DDS: true` in BetterDiscord `data/stable/plugins.json` while Discord is stopped. Preserve settings of all other plugins; reject malformed/unsafe JSON; use atomic replacement.
- Start installed Discord and DDS Companion after installing four component floors. Await actual processes, fresh `RUNNING` DDS 0.5.3 plugin heartbeat, and SQLite archive header before claiming `RUNTIME_READY`.
- If startup/heartbeat fails: explicit `FIRST_START_INCOMPLETE`, no fake green status; leave installed files and DataRoot intact.
- Make menu 1 explicitly «only download, not install» and menu 8 explicitly «automated DDS installation».
- Keep existing numeric approvals (`1` approve / `0` cancel), stage checks, hashing, data preservation, independent floor ordering.

NOT CHANGED: GUI, EXE icon, Repair architecture, Updater, plugin payload, Companion payload, DDS archive, GitHub release. No direct Windows live Setup evidence yet.
