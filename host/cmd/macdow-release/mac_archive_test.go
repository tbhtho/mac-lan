package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func macArchiveFixture(t *testing.T, host, provenance []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	archive := tar.NewWriter(gz)
	for _, entry := range []struct {
		name string
		data []byte
		mode int64
	}{{"macos-arm64/MacDowHost", host, 0755}, {"macos-arm64/BUILD-PROVENANCE.json", provenance, 0644}} {
		if entry.data == nil {
			continue
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Size: int64(len(entry.data)), Mode: entry.mode, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func menuArchiveFixture(t *testing.T, change func(map[string][]byte)) []byte {
	t.Helper()
	host := make([]byte, 64)
	binary.LittleEndian.PutUint32(host, 0xfeedfacf)
	binary.LittleEndian.PutUint32(host[4:], 0x0100000c)
	binary.LittleEndian.PutUint32(host[12:], 2)
	files := map[string][]byte{
		"Contents/MacOS/mac-lan": host, "Contents/Helpers/MacDowHost": host,
		"Contents/Info.plist":                      []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.tbhtho.macdow.host</string><key>CFBundleExecutable</key><string>mac-lan</string><key>CFBundleName</key><string>mac-lan</string><key>LSUIElement</key><true/></dict></plist>`),
		"Contents/Resources/MAC-DOW-INSTALL.json":  []byte(`{"product":"MAC-DOW","repositoryId":1430459188,"version":"0.1.0-alpha.15","os":"darwin","arch":"arm64","commit":"cccccccccccccccccccccccccccccccccccccccc","layout":"app-bundle"}`),
		"Contents/Resources/COMMIT.txt":            []byte(strings.Repeat("c", 40)),
		"Contents/Resources/BUILD-PROVENANCE.json": []byte(`{"publicUpdate":true,"host":"cross-compiled","nativeUI":"compiled on first Mac launch"}`),
	}
	if change != nil {
		change(files)
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarball := tar.NewWriter(gz)
	for name, data := range files {
		if data == nil {
			continue
		}
		if err := tarball.WriteHeader(&tar.Header{Name: "mac-lan.app/" + name, Size: int64(len(data)), Mode: 0755, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPublicMacMenuArchiveGate(t *testing.T) {
	if err := validatePublicMacArchive(bytes.NewReader(menuArchiveFixture(t, nil))); err != nil {
		t.Fatal("public menu app refused", err)
	}
	for _, test := range []struct {
		name, file string
		contents   []byte
	}{
		{"missing host", "Contents/Helpers/MacDowHost", nil},
		{"wrong format", "Contents/MacOS/mac-lan", []byte("not arm64 Mach-O")},
		{"wrong commit", "Contents/Resources/COMMIT.txt", []byte(strings.Repeat("d", 40))},
		{"private provenance", "Contents/Resources/BUILD-PROVENANCE.json", []byte(`{"publicUpdate":false}`)},
		{"credential in readme", "Contents/Resources/README.md", []byte("https://discord.com/api/webhooks/123456789012345678/" + strings.Repeat("fake_TOKEN-", 7))},
		{"case collision", "Contents/Resources/commit.txt", []byte("duplicate")},
		{"extra content", "Contents/Resources/user-settings.json", []byte("{}")},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := menuArchiveFixture(t, func(files map[string][]byte) { files[test.file] = test.contents })
			if err := validatePublicMacArchive(bytes.NewReader(data)); err == nil {
				t.Fatal("accepted malformed menu app")
			}
		})
	}
}

func TestPublicMacMenuArchiveRejectsUnrelatedEmptyDirectory(t *testing.T) {
	original, err := gzip.NewReader(bytes.NewReader(menuArchiveFixture(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	input := tar.NewReader(original)
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	output := tar.NewWriter(gz)
	for {
		header, err := input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := output.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(output, input); err != nil {
			t.Fatal(err)
		}
	}
	if err := output.WriteHeader(&tar.Header{Name: "mac-lan.app/Contents/Resources/unrelated/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validatePublicMacArchive(bytes.NewReader(out.Bytes())); err == nil {
		t.Fatal("accepted unrelated empty app directory")
	}
}

func TestPublicMacArchiveGate(t *testing.T) {
	fakeCredential := []byte("https://discord.com/api/webhooks/123456789012345678/" + strings.Repeat("fake_TOKEN-", 7))
	wrappedCredential := append(bytes.Repeat([]byte{'x'}, 64*1024-22), fakeCredential...)
	wrappedCredential = append(wrappedCredential, []byte("\x00binary string table suffix")...)
	for _, test := range []struct {
		name             string
		host, provenance []byte
		wantError        bool
	}{
		{"public", []byte("native host bytes"), []byte(`{"publicUpdate":true,"host":"cross-compiled"}`), false},
		{"private", []byte("native host bytes"), []byte(`{"publicUpdate":false}`), true},
		{"missing marker", []byte("native host bytes"), []byte(`{"host":"cross-compiled"}`), true},
		{"missing provenance", []byte("native host bytes"), nil, true},
		{"missing host", nil, []byte(`{"publicUpdate":true}`), true},
		{"wrapped credential", wrappedCredential, []byte(`{"publicUpdate":true}`), true},
		{"credential in metadata", []byte("native host bytes"), append(append([]byte(`{"publicUpdate":true,"note":"`), fakeCredential...), []byte(`"}`)...), true},
		{"generic validation regex", []byte(`^https://discord\.com/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]+$`), []byte(`{"publicUpdate":true}`), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePublicMacArchive(bytes.NewReader(macArchiveFixture(t, test.host, test.provenance)))
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected public archive result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), string(fakeCredential)) {
				t.Fatal("credential included in error")
			}
		})
	}
}

func TestPublicMacArchiveRejectsLinksAndInvalidCompression(t *testing.T) {
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	archive := tar.NewWriter(gz)
	if err := archive.WriteHeader(&tar.Header{Name: "macos-arm64/MacDowHost", Typeflag: tar.TypeSymlink, Linkname: "/private/host", Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	archive.Close()
	gz.Close()
	if err := validatePublicMacArchive(bytes.NewReader(output.Bytes())); err == nil {
		t.Fatal("accepted link")
	}
	if err := validatePublicMacArchive(strings.NewReader("not gzip")); err == nil {
		t.Fatal("accepted invalid gzip")
	}
	valid := macArchiveFixture(t, []byte("host"), []byte(`{"publicUpdate":true}`))
	valid[len(valid)-1] ^= 1
	if err := validatePublicMacArchive(bytes.NewReader(valid)); err == nil {
		t.Fatal("accepted corrupt gzip")
	}
}

func TestPrivateCredentialScannerAcrossShortReads(t *testing.T) {
	credential := "https://discord.com/api/webhooks/123456789012345678/" + strings.Repeat("fake_TOKEN-", 7)
	if err := scanPrivateCredentials(&shortReader{strings.NewReader("prefix" + credential + "suffix")}); err == nil {
		t.Fatal("missed split credential")
	}
}

type shortReader struct{ io.Reader }

func (s *shortReader) Read(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return s.Reader.Read(p)
}
