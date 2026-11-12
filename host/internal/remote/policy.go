// Package remote runs commands and moves files between a paired Windows PC and
// Mac over mac-lan's existing pinned TLS listener, and exposes that link to
// local tools (the terminal window, a command-line shell and an MCP server).
package remote

import (
	"regexp"
	"strings"
)

// A command that matches one of these patterns is destructive or privileged and
// runs only after a person confirms it in mac-lan on the computer that sent it.
// This is a safety net against mistakes, not a sandbox: a remote command runs
// with the full rights of the user who enabled remote commands on that computer.
//
// A rule with "unless" is judged one command at a time (the text between ; & |
// and parentheses): a command that matches unless only reads state and passes.
var confirmPatterns = []struct {
	pattern *regexp.Regexp
	reason  string
	unless  *regexp.Regexp
}{
	{regexp.MustCompile(`(^|[\s;&|(` + "`" + `])(sudo|doas|runas|su)(\.exe)?(\s|$)`), "runs with administrator rights", nil},
	{regexp.MustCompile(`-verb\s+runas`), "runs with administrator rights", nil},
	{regexp.MustCompile(`(^|[\s;&|(/\\])rm(\.exe)?\s+(.*\s)?-{1,2}[a-z-]*(r|f|recursive|force)`), "deletes files", nil},
	{regexp.MustCompile(`(^|[\s;&|(])(rmdir|rd)\s+(.*\s)?/s`), "deletes a folder tree", nil},
	{regexp.MustCompile(`(^|[\s;&|(])(del|erase)\s+(.*\s)?/[sqf]`), "deletes files", nil},
	{regexp.MustCompile(`(remove-item|\bri\b|\brm\b).*-(recurse|force)`), "deletes files", nil},
	// Only at the start of a command, so the word inside a quoted message does not match.
	{regexp.MustCompile(`(^|[;&|(]\s*)(shred|mkfs[.\w]*|dd|fdisk|diskpart|format(\.com)?|format-volume|clear-disk|cipher\s+/w)(\s|$)`), "erases or rewrites a disk", nil},
	{regexp.MustCompile(`diskutil\s+(erase|partition|apfs\s+delete|zerodisk|secureerase|reformat)`), "erases or rewrites a disk", nil},
	{regexp.MustCompile(`(^|[\s;&|(])(shutdown|reboot|halt|poweroff|restart-computer|stop-computer|logoff)(\.exe)?(\s|$)`), "shuts down or restarts the computer", nil},
	{regexp.MustCompile(`git\s+(.*\s)?push\s+(.*\s)?(-f\b|--force)`), "overwrites remote Git history", nil},
	{regexp.MustCompile(`git\s+(.*\s)?(reset\s+(.*\s)?--hard|clean\s+(.*\s)?-[a-z]*f|branch\s+(.*\s)?-d\b|checkout\s+(.*\s)?--\s|restore\s+(.*\s)?\.|filter-branch|filter-repo|stash\s+(drop|clear))`), "discards Git work", nil},
	{regexp.MustCompile(`gh\s+repo\s+(delete|archive|rename)`), "changes a GitHub repository", nil},
	{regexp.MustCompile(`(^|[\s;&|(])(killall|pkill|taskkill|stop-process)(\.exe)?(\s|$)`), "ends running programs", nil},
	{regexp.MustCompile(`(^|[\s;&|(])kill\s+(.*\s)?-(9|kill)\b`), "ends running programs", nil},
	{regexp.MustCompile(`(^|[\s;&|(])(launchctl|csrutil|spctl|tccutil|dscl|nvram|pmset|systemsetup|scutil|defaults\s+delete|security\s+(delete|add|import|unlock))`), "changes macOS system settings",
		regexp.MustCompile(`^\s*(launchctl\s+(list|print|print-cache|print-disabled|blame|procinfo|hostinfo|managername|managerpid|manageruid|version|help|getenv)\b|csrutil\s+status\b|spctl\s+(--status|--assess|-a)\b|dscl\s+\S+\s+-?(read|list|search|readall)\b|nvram\s+-p\b|pmset\s+-g\b|systemsetup\s+-(get|list|help|print)\w*\b|scutil\s+(--get|--dns|--proxy|--nwi|-r)\b)[^>]*$`)},
	{regexp.MustCompile(`(^|[\s;&|(])(reg(\.exe)?\s+(add|delete|import)|bcdedit|netsh|sc(\.exe)?\s+(delete|config|stop|create)|schtasks\s+.*/(create|delete|change)|net(\.exe)?\s+(user|localgroup)|set-executionpolicy|new-service|set-itemproperty\s+.*hk)`), "changes Windows system settings",
		regexp.MustCompile(`^\s*(netsh\s+(.*\s)?(show|dump)\b|bcdedit(\s+/enum\b.*|\s+/v)?\s*$|net(\.exe)?\s+(user|localgroup)\s*$)[^>]*$`)},
	{regexp.MustCompile(`(^|[\s;&|(])(chown|chmod)\s+(.*\s)?-[a-z]*r`), "changes permissions on a folder tree", nil},
	{regexp.MustCompile(`(curl|wget|iwr|invoke-webrequest|irm|invoke-restmethod)\b.*\|\s*(sh|bash|zsh|iex|invoke-expression|python\d?|node|pwsh|powershell)\b`), "downloads and runs a script", nil},
	{regexp.MustCompile(`(^|[\s;&|(])(iex|invoke-expression)(\s|\()`), "runs generated script text", nil},
	{regexp.MustCompile(`>\s*/dev/(sd|disk|rdisk|nvme)`), "writes directly to a disk", nil},
	{regexp.MustCompile(`:\(\)\s*\{`), "starts a fork bomb", nil},
	{regexp.MustCompile(`(drop|truncate)\s+(database|table|schema)`), "deletes database contents", nil},
	{regexp.MustCompile(`(docker|podman)\s+(system\s+prune|volume\s+(rm|prune)|rm\s+.*-f)`), "deletes container data", nil},
	{regexp.MustCompile(`(npm|pnpm|yarn|cargo|twine|gem|dotnet\s+nuget)\s+(publish|push|unpublish|yank)`), "publishes or removes a package", nil},
}

var commandBoundary = regexp.MustCompile("[;&|()`]+")

// NeedsConfirmation returns a short reason when command must be confirmed by a
// person before it runs, or "" for an ordinary command.
func NeedsConfirmation(command string) string {
	text := strings.ToLower(strings.Join(strings.Fields(command), " "))
	var parts []string
	for _, rule := range confirmPatterns {
		if !rule.pattern.MatchString(text) {
			continue
		}
		if rule.unless == nil {
			return rule.reason
		}
		if parts == nil {
			parts = commandBoundary.Split(text, -1)
		}
		for _, part := range parts {
			// Each part is tested as a command of its own, so a reading command
			// cannot carry a changing one past the rule.
			if rule.pattern.MatchString(part) && !rule.unless.MatchString(part) {
				return rule.reason
			}
		}
	}
	return ""
}
