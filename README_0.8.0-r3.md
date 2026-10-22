# DDS Installer 0.8.0-r3 — Windows x64 candidate

**Local development candidate. NOT RELEASED. Real Windows first-setup validation pending.**

Single-file console installer `DDSSetup.exe`. Extract the ZIP and launch `DDSSetup.exe` (no command-line typing required).

Menus: `1` = DOWNLOAD/VERIFY ONLY (no installation); `8` = AUTOMATED DDS SETUP. Two numeric approvals remain: `1` confirm, `0` cancel. No Russian confirmation words.

In Setup, the installer acquires/verifies packages, installs missing Discord + BetterDiscord + DDS Plugin + DDS Companion, and stops Discord when required. **Setup may forcibly close Discord** after a graceful attempt; save any drafts and leave voice conversations before confirming installation.

In a new first Setup, DDS Plugin is enabled in BetterDiscord's own `plugins.json` without enabling unrelated plugins. At the end the installer starts Discord and Companion and waits for DDS Plugin RUNNING heartbeat and an accessible SQLite header. It prints `RUNTIME_READY` only if those runtime checks pass; otherwise `FIRST_START_INCOMPLETE` with a reason.

**Important constraints:** This remains first-install setup, NOT Repair. It blocks ambiguous PARTIAL/Portable states; already healthy floors are not reinstalled. Installing on a system with all four floors already detected will do nothing, even if the user intentionally turned the plugin off. Existing local archive `%LOCALAPPDATA%\DDS_Companion` is preserved. No real Windows full Setup/Repair or GUI acceptance is claimed.

Source basis: exact frozen r2 source archive SHA-256 `7662bc242bf600dd31a088c55b56609c465fc170748b96d992f2633b42f2110c`.
