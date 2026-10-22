# DDS Installer 0.7.0-r1 — Fresh Companion File Install

Date: 2026-10-10. Previous immutable candidate: 0.6.0-r1.

## Scope — one new installation floor

- Added menu option `7`: explicit per-user first installation of DDS Companion into a **new** `%LOCALAPPDATA%\DDS` directory, never into an existing directory.
- Only consumes a locally verified full Stable Companion staging receipt, with authenticated bootstrap SHA-256 and internal package manifest; copies to a sibling temporary directory on the target filesystem, re-verifies staged and copied bytes, and publishes the full directory in one rename.
- Requires exact confirmation `УСТАНОВИТЬ` and two no-running-DDS checks (before copying and before publishing). Refuses existing, partial/uncertain or Portable profile detection.
- Refuses overlap with known DataRoot. Does not touch SQLite, DataRoot, Discord, BetterDiscord, Plugin, updater protocol, registry, autostart or existing installed DDS.
- Failed ordinary operations clean their temporary scratch directory. No automatic launch. The truthful result is `FILES_INSTALLED_UNSTARTED`, not an accepted/running DDS deployment.

## Deliberate limits

- Does not install Discord/BetterDiscord/Plugin or perform full first-use/GUI/archive validation. That dependency chain remains open.
- A hard kill during scratch preparation can leave a uniquely named `.dds-installer-fresh-*` temporary directory. Never deletes such orphaned directories automatically: they may require separate, explicitly bounded cleanup after validation.
- No production machine installation, Windows LIVE first-install test, physical power-loss durability, GitHub release or component promotion.
