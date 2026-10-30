# mac-lan 0.1.0-alpha.39

Send a selected file from Mac to your paired Windows computer. Choose **Send File to Windows…** from the Mac menu, then **Files → Receive from Mac** on Windows and pick a new destination filename. Update both apps to use this feature.

Transfers show progress, support cancellation, and verify the received bytes before saving. Existing files are never overwritten, and originals remain on the Mac. Send one file at a time; compress Mac folders to ZIP first. The existing Windows-to-Mac files/folders flow and remote control remain available.

Validation: native Mac build/self-tests and Go race regressions passed. The full Windows .NET 10 suite, Go tests/vet, and production WPF build passed with zero build warnings/errors. The real receiver passed 38 loopback TLS checks on Windows, and 23 hidden WPF control/layout checks passed without showing windows or touching an active session. Two existing linked-directory tests were skipped because link creation was unavailable on the Windows account. Real two-computer transfers and interactive save-picker acceptance remain untested.
