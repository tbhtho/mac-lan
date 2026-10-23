# Desktop usability and automatic diagnostics

User wants existing Windows/Mac tabs and connection concept retained, with professional visual execution, clear pairing state, no blocked application title bars, and automatic diagnostics to their specified Discord destination. They provided screenshots and a Mac log showing permissions granted, a running private listener, idle awaiting pairing, and unrelated private-update access failure. Never include their temporary token, webhook credential, personal/email/host names, screen contents or exact addresses in reports/source.

Implementation choices: retain WPF; a separate slim OS AppBar reserves space for the Windows/Mac tabs and is removed when the app quits or uses App Priority. Connection window has a coherent light surface, blue selected tabs and action buttons, consistent Segoe UI sizing, visible focus, restrained borders and readable dark text. Same actions, with unavailable stream controls disabled until paired and technical stream settings under disclosure. Empty discovery states identify older command-line hosts rather than referring to an unavailable app.

Mac CLI host will also advertise bounded Bonjour service records and accept guided pairing, using native user-approved dialogs with a Terminal fallback. TLS pinning, separate Mac viewing consent and explicit Windows control remain. The current no-capture line is clarified as a waiting status, not treated as an error.

Diagnostics: latest user direction restricts remote reports to macOS. Windows logs stay local, and its binary does not contain the destination. The shared Go reporter enforces both mac role and Darwin runtime. Mac startup/pair/permission/capture reports contain allowlisted codes/counters, bounded local retention, background delivery and bounded retries. Webhook is private Mac build configuration outside Git; never tracked source or displayed logs. The initial investigation report was delivered before the Mac-only refinement.

Latest UI refinements: remove manual connection details entirely. Preserve native consent on the Mac but no main app window: menu-bar item appears on launch and starts listening automatically if existing viewing permission is granted. No startup service or automatic capture. Only explicit pairing/viewing requests open dialogs.

Steps:
- [x] Regression tests for consent/discovery advertisement sanitization, diagnostic exclusions/retry, state gating and bar geometry.
- [x] Implement CLI advertisement + guided native consent; retain synthetic-only tests.
- [x] Add private-config Mac diagnostic reporter; Windows logging stays local.
- [x] Theme WPF and separate reserved tab bar from setup/remote content; remove manual connection section.
- [x] Full headless suite, Windows UI inspection, AppBar work-area restoration check and independent code review. Further computer-use testing stopped at the user's request.
- [ ] Publish scoped prerelease under the user's existing release authorization; verify exact asset hashes. Mac Go remains cross-compiled unless native access becomes available; do not claim native .app acceptance.
