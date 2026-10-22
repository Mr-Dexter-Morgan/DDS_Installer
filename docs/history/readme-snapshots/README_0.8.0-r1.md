# DDS Installer 0.8.0-r1 — local development candidate

Windows x64 console binary: `DDSSetup.exe`. Menu option **8** is an **initial Installed-profile Setup only**, *not* repair or Portable migration.

1. Start on a **disposable Windows environment**, not the working DDS installation.
2. Read the Detect/Plan summary. Choose option 8.
3. Confirm `ПОДГОТОВИТЬ`. The Installer downloads/verifies/stages only the missing/blocked floors.
4. After all required packages and exact identities pass, confirm `УСТАНОВИТЬ ВСЁ` to allow real installation.
5. Discord official installer may require normal interactive steps; BetterDiscord uses official stable CLI. If Discord reopens, close it when prompted before Plugin Apply.
6. Inspect the per-component result. `FILES_INTEGRATED_FIRST_START_PENDING` confirms only installed files/post-detection. Companion first GUI launch, plugin runtime heartbeat, DDS_Data link and clean-machine acceptance are not proven by this result.

Guards: PARTIAL or Portable state is BLOCKED rather than overwritten; installed working applications/data are not the test target; staged assets must match user-confirmed SHA-256; previous Plugin rollback behavior remains. No automatic repair/uninstall or unapproved GitHub writes.

Source base: archived DDS Installer `0.7.1-r1` source ZIP with SHA-256 `265fae22e573984718b1feaa99a74b2df49334a0dd6dc66c7d89280af24a36e9`. 

BetterDiscord upstream documentation: https://github.com/BetterDiscord/cli/blob/main/README.md (install `--channel stable`, `--silent` globally).
