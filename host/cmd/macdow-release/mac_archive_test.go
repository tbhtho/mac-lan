package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
