DDS Installer 0.6.0-r1 — recovery candidate (NOT RELEASED)

DDSSetup.exe is the Windows installer candidate. Operation 6 is MANUAL
recovery only; never use it on the actual installed DDS for initial LIVE gate.
Do not approve operation 5 or 6 on a real installation without a separate,
explicit decision and verified rollback readiness.

To validate on Windows, run the separate Isolated-Recovery-Gate kit.
It performs Go unit tests only in temporary isolated directories.
The test kit executable is NOT the production installer.
