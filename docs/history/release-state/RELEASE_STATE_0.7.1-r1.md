# DDS Installer 0.7.1-r1 — release state

**LOCAL DEVELOPMENT CANDIDATE / SANDBOX PASS / WINDOWS X64 CROSS-BUILD PASS / PLUGIN ROLLBACK WINDOWS LIVE PENDING / NOT RELEASED.**

Only the Plugin Apply post-validation failure/rollback safety and two related path guards are modified from 0.7.0-r1. Existing Companion 0.7.0-r1 isolated first-install and real 0.8.5 startup Windows evidence remains valid for that historical candidate, but has not been rerun on this exact new binary.

The next gate is the isolated Windows Plugin Apply test with synthetic roots only. Working DDS and Plugin 0.5.3 must not be modified by that gate. No Installer GitHub publication has been authorized.
