# DDS Installer 0.8.0-r3 — Validation

**Status:** automated validation only. Windows live setup has NOT been performed with r3.

Validation gate: 122 top-level Go tests PASS, targeted tests for safe numeric confirmations and runtime orchestration, race PASS, vet PASS, Windows x64 PE32+ build PASS, and Windows test-binary compilation PASS. Runtime integration is verified through injected hooks and fake filesystem settings; real Discord / BetterDiscord / Plugin / Companion live-first-start remains pending.

Main scenarios covered:
- preserve other plugin enable flags and settings when enabling DDS;
- create missing `plugins.json`; reject malformed settings; safe no-op if DDS already enabled; allow DDS only after Setup consent;
- skip closing Discord if not running, close it when running, fail when close command fails;
- launch first-start in order and refuse ready state if Discord fails, heartbeat missing/stale, SQLite header invalid;
- previous first-setup and stage preflight tests remain green.

Not tested by automation: official Discord/BetterDiscord installer execution and process lifecycle on the user's Windows; actual BetterDiscord live loading; first GUI opening; tray and autostart; end-to-end archive import; installer GUI and EXE icon.
