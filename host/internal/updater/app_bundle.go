package updater

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ValidateMacAppBundle checks the portable menu-bar package shape and build
// identity. It does not replace the signed-feed and digest checks. Cross-platform
// package tools can omit POSIX permission checks while still checking Mach-O.
func ValidateMacAppBundle(root string, requireExecutable bool) error {
	var total int64
	count := 0
	allowedDirs := map[string]bool{".": true, "Contents": true, "Contents/MacOS": true, "Contents/Helpers": true, "Contents/Resources": true, "Contents/Resources/licenses": true}
	allowedFiles := map[string]bool{
		"Contents/Info.plist": true, "Contents/MacOS/mac-lan": true, "Contents/Helpers/MacDowHost": true,
		"Contents/Resources/MAC-DOW-INSTALL.json": true, "Contents/Resources/COMMIT.txt": true,
		"Contents/Resources/BUILD-PROVENANCE.json": true, "Contents/Resources/README.md": true,
		"Contents/Resources/LICENSE": true, "Contents/Resources/THIRD_PARTY_NOTICES.md": true,
	}
	for _, name := range []string{"zeroconf-LICENSE.txt", "webview2-NOTICE.txt", "webview2-LICENSE.txt", "miekg-dns-LICENSE.txt", "gorilla-websocket.txt", "golang-x-LICENSE.txt", "go-LICENSE.txt"} {
		allowedFiles["Contents/Resources/licenses/"+name] = true
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		if count > 512 || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("Mac app contains links or too many files")
		}
		if info.IsDir() {
			if !allowedDirs[rel] {
				return errors.New("Mac app contains an unrelated directory")
			}
			return nil
		}
		if !info.Mode().IsRegular() || !allowedFiles[rel] || info.Size() > 128<<20 {
			return errors.New("Mac app contains unrelated or oversized content")
		}
		total += info.Size()
		if total > 256<<20 {
			return errors.New("Mac app exceeds package limits")
		}
		return nil
	}); err != nil {
		return err
	}
	var marker Marker
	if err := readJSON(filepath.Join(metadataRoot(root), "MAC-DOW-INSTALL.json"), &marker); err != nil {
		return errors.New("missing Mac app identity")
	}
	if marker.Product != "MAC-DOW" || marker.RepositoryID != BuildConfig().RepositoryID || marker.OS != "darwin" || marker.Arch != "arm64" || marker.Layout != "app-bundle" || !feedCommit.MatchString(marker.Commit) {
		return errors.New("invalid Mac app identity")
	}
	if _, err := ParseVersion(marker.Version); err != nil {
		return errors.New("invalid Mac app version")
	}
	commit, err := os.ReadFile(filepath.Join(metadataRoot(root), "COMMIT.txt"))
	if err != nil || strings.TrimSpace(string(commit)) != marker.Commit {
		return errors.New("Mac app commit marker mismatch")
	}
	for _, name := range []string{"Contents/MacOS/mac-lan", "Contents/Helpers/MacDowHost"} {
		if err := validateMacHost(filepath.Join(root, filepath.FromSlash(name)), requireExecutable); err != nil {
			return err
		}
	}
	info, err := os.ReadFile(filepath.Join(root, "Contents", "Info.plist"))
	if err != nil || len(info) > 64<<10 {
		return errors.New("missing or oversized Mac app Info.plist")
	}
	return validateMacAppInfo(info)
}

type plistNode struct {
	XMLName xml.Name
	Text    string      `xml:",chardata"`
	Nodes   []plistNode `xml:",any"`
}

func validateMacAppInfo(data []byte) error {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var plist plistNode
	if err := decoder.Decode(&plist); err != nil || plist.XMLName.Local != "plist" || len(plist.Nodes) != 1 || plist.Nodes[0].XMLName.Local != "dict" {
		return errors.New("invalid Mac app Info.plist")
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid Mac app Info.plist")
		}
		if text, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(text)) != "" {
			return errors.New("trailing Mac app Info.plist content")
		}
	}
	entries := plist.Nodes[0].Nodes
	if len(entries)%2 != 0 {
		return errors.New("invalid Mac app Info.plist dictionary")
	}
	values := map[string]plistNode{}
	for i := 0; i < len(entries); i += 2 {
		key := entries[i]
		if key.XMLName.Local != "key" || len(key.Nodes) != 0 || strings.TrimSpace(key.Text) == "" {
			return errors.New("invalid Mac app Info.plist key")
		}
		if _, exists := values[key.Text]; exists {
			return errors.New("duplicate Mac app Info.plist key")
		}
		values[key.Text] = entries[i+1]
	}
	for key, expected := range map[string]string{"CFBundleIdentifier": "com.tbhtho.macdow.host", "CFBundleExecutable": "mac-lan", "CFBundleName": "mac-lan"} {
		value := values[key]
		if value.XMLName.Local != "string" || len(value.Nodes) != 0 || value.Text != expected {
			return errors.New("Mac app branding or executable identity mismatch")
		}
	}
	if display, exists := values["CFBundleDisplayName"]; exists && (display.XMLName.Local != "string" || display.Text != "mac-lan" || len(display.Nodes) != 0) {
		return errors.New("Mac app display name mismatch")
	}
	if background := values["LSUIElement"]; background.XMLName.Local != "true" || strings.TrimSpace(background.Text) != "" || len(background.Nodes) != 0 {
		return errors.New("Mac app must be a menu-bar agent")
	}
	return nil
}
