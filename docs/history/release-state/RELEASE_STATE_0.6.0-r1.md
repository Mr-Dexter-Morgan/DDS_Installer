# DDS Installer 0.6.0-r1 — Current candidate state

**IMPLEMENTED / SANDBOX AUTOMATED PASS / WINDOWS LIVE TEST PENDING / NOT PUBLISHED.**

Scope: durable pending transaction marker, explicit recovery command with strict verification and confirmation, fail-closed refusal of new Apply if a transaction is unfinished.

Predecessor 0.5.0-r1: Windows isolated 6/6 real filesystem tests PASS; actual live DDS replacement/rollback still not tested. Its source/binaries/evidence remain immutable.

Required next gate: Windows isolated recovery-test executable in a synthetic `%TEMP%` sandbox, then user review. Do not apply to installed Companion without a further, separately approved gate.

No GitHub tag/release and no live Windows DDS mutation performed.
