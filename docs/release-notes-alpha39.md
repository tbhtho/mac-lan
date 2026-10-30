# mac-lan 0.1.0-alpha.39

Send a selected file from Mac to your paired Windows computer. Choose **Send File to Windows…** from the Mac menu, then **Files → Receive from Mac** on Windows and pick a new destination filename. Update both apps to use this feature.

Transfers show progress, support cancellation, and verify the received bytes before saving. Existing files are never overwritten, and originals remain on the Mac. Send one file at a time; compress Mac folders to ZIP first. The existing Windows-to-Mac files/folders flow and remote control remain available.

Validation: native Mac build and headless self-tests, Go race regressions, and 38 isolated receiver checks on Mac/.NET 8 passed. Windows .NET 10 production build/tests and real two-computer transfer validation are pending; replace this sentence with actual results before publication.
