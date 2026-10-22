# DDS Installer 0.8.0-r2 — numeric confirmations

**Status: local development candidate; Windows LIVE test pending; NOT RELEASED.**

Scope: console input only. All confirmation actions now accept **1 = confirm**, **0 = cancel**. Enter, any other digit, Russian words, malformed input, and EOF cancel. The normal main menu still uses digits 1–8; Enter exits.

Applies to: Plugin Apply, Companion upgrade, Companion interrupted-transaction recovery, Companion fresh install, first Setup staging, first Setup installation, and the prompt to continue after manually closing Discord.

**Option 8 requires two separate approvals:** 1 before download/staging and 1 before executing installation. Merely selecting 8 does not install anything.

No other product behavior was changed. In particular, the installer is still a **console application**, and it **does not automatically close Discord** before Plugin Apply. It still asks the user to close Discord, then enter 1 to continue. Full Setup/Repair and GUI still require separate work. Do **not** mistake this candidate for complete DDS 1.0.

Normal run on Windows: extract ZIP to a folder and run `DDSSetup.exe`. Running from CMD is unnecessary. Menu and prompts require no Russian typing. Do not use option 8 on an existing working DDS installation: Installed/Portable/PARTIAL behavior is unchanged from 0.8.0-r1.

Source provenance: DDS Installer 0.8.0-r1 source ZIP, SHA-256 `40443d845392840c4131d41e37ac6c6a96fe2b53447deb46fa731403298a523d`.
