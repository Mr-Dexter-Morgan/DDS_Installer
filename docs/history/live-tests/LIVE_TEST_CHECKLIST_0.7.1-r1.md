# DDS Installer 0.7.1-r1 — isolated Windows Plugin Apply gate

1. Download the exact `DDS-Installer-0.7.1-r1-PluginApply-Isolated-Windows-Gate.zip` and check its SHA-256 (see adjacent `.sha256`). Extract to a NEW temporary folder.
2. Run `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-PluginApply-Isolated-Windows-Gate.ps1` from the extracted folder.
3. Require `GATE_EXE_SHA256_OK`, critical regression tests PASS, `TEST_EXIT=0`, `ISOLATED_PLUGIN_APPLY_WINDOWS_PASS`.
4. Confirm tests use only `Go testing.TempDir` synthetic APPDATA/BetterDiscord/Plugin and sentinel DataRoot. No real Plugin/Discord/Companion modification.
5. Remove ONLY this isolated test folder after an independently checked PASS; record actual console output in a new LIVE report. Do not promote a product release based on this isolated gate alone.
