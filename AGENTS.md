# Repository guidance

## Preserve the working project

Read the current Git state and relevant implementation before editing. Preserve unrelated work, existing releases, user settings, and local artifacts. Do not rewrite published history or change repository visibility as a presentation cleanup. Use plain, natural commit subjects without conventional prefixes. A new source commit does not automatically require a binary release.

## Keep the public introduction useful

Maintain the concise README style established by the `github-repo-presentation` skill: one purpose sentence, factual platform/license badges, verified downloads, short launch instructions, meaningful feature coverage, and links to deeper documentation. Use ordinary punctuation and explicit UTF-8. Do not add a changelog or session transcript to the top of the README.

Update the README when a commit changes user-visible behavior, supported platforms, prerequisites, launch steps, downloads, or material limits. Internal changes need no README churn. Keep engineering detail and historical context in `docs/development.md` and the relevant focused documents. Do not drop product capabilities or legal notices merely to shorten the page.

Use genuine project screenshots only after inspecting them. Keep existing useful showcase images and descriptive captions. If there are no suitable screenshots, omit the gallery; do not fabricate one, borrow another project's images, or launch the app just to fill the gap. Honor the user's preference for headless validation and their own two-computer testing. Never claim that a mockup or source change proves released behavior.

Read the actual `LICENSE` before describing it. Do not change license terms as part of a README edit. Keep third-party notices with distributed artifacts. Do not invent CI status, successful runs, download counts, performance measurements, or verified hardware support. A manual workflow is not an automatically passing CI badge.

## Downloads and updates

The user explicitly authorized renaming the existing repository to `tbhtho/mac-lan`, making it public, and restoring automatic updates. That request supersedes the earlier manual-only distribution decision. Preserve the same repository identity (1430459188) and source history. Publish public downloads and the signed update feed only from this repository; do not recreate a separate distribution repository. Future visibility or history changes require new explicit authorization.

Coordinate release assets and the signed feed: publish all required public artifacts before the matching feed, never replace published assets under an existing version, and verify anonymous access. Preserve the alpha.14-compatible feed asset names; the canonical app archive and its legacy feed alias must be byte-identical. Verify the release destination, exact asset names, byte counts, hashes, platform, architecture, version, and source commit before advertising a new release as available. Source commits and release artifacts must identify the same intended commit; package provenance must truthfully distinguish native execution from cross-compilation and headless tests from real hardware checks. Keep the Ed25519 private key, diagnostic destination, tokens, and all credentials out of version control and public artifacts. No signing key or credential may be printed in logs.

Keep the public app name exactly `mac-lan`. Preserve legacy internal identifiers, settings/cache paths, and the `MacDowHost` compatibility executable when changing branding would disturb permissions or existing user data. A configured 60 fps target is not proof of measured throughput or responsiveness. Keep the cross-compiled menu-bar app launcher, first-launch native AppKit/capture compilation, command-line compatibility bridge, and actual native execution evidence distinct. The app may need deliberate Screen Recording and Accessibility grants under its own identity. Preserve macOS permission requirements and OS security protections. Do not start paid CI, install toolchains, bypass OS protections, or change account permissions as incidental verification.

Before completing a documentation or release-presentation change, check local document/image links, review the diff for lost prerequisites or feature coverage, and verify new external release claims through authorized reads. Report an unavailable preview, native test, or download check honestly. Do not broaden a reversible prose edit into an application test suite unless behavior changed.
