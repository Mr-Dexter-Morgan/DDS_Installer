# DDS Installer 0.7.1-r1 — Plugin Apply validation rollback safety

**Scope:** Narrow patch of the existing Plugin Apply/Validate/rollback floor from Installer 0.7.0-r1. This is **not** a new Plugin installer or a clean-machine Setup feature.

- Replace fixed `.failed-apply` rollback parking path with a unique private temporary directory. A pre-existing unrelated `DDS.plugin.js.failed-apply` can no longer block restoring the previous file on Windows.
- On a deliberately failed post-install validation, remove the candidate from the target path before restoring the backup. With no previous plugin, leave the target absent. Preserve both paths and report failure if rollback itself cannot finish.
- Reject symlinked APPDATA and symlinked Plugin staging package/payload directories.
- Add four plugin tests: existing-marker rollback for replacement and fresh install, symlinked payload directory, symlinked APPDATA.
- Version updated to 0.7.1-r1. No changes to DDS Companion, DDS Plugin source, capture, SQLite, DataRoot, BetterDiscord runtime, GitHub, or any production installation.
