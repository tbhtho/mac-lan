package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxTransferBytes   int64 = 100 << 30
	maxTransferEntries       = 10000
	maxTransferDepth         = 32
	transferChunk            = 256 << 10
)

var errTransfer = errors.New("transfer could not be completed")

// A transfer capability exists only for the current, locally approved pairing.
// Bulk uploads use a separate TLS request and never enter the video/input queues.
type transferCredential struct {
	token  string
	peer   string
	ctx    context.Context
	cancel context.CancelFunc
}

type transferReceiver struct {
	outgoing   *outgoingFile
	root       string
	mu         sync.Mutex
	credential *transferCredential
	busy       bool
	activeDone chan struct{}
	stopped    bool
}

type transferReceipt struct {
	OK     bool   `json:"ok"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func newTransferReceiver(root string) *transferReceiver { return &transferReceiver{root: root} }

func (t *transferReceiver) grant(peer string) (string, func(), error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	credential := &transferCredential{token: hex.EncodeToString(secret), peer: peer, ctx: ctx, cancel: cancel}
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		cancel()
		return "", nil, errTransfer
	}
	if t.credential != nil {
		t.credential.cancel()
	}
	t.credential = credential
	t.mu.Unlock()
	var once sync.Once
	revoke := func() {
		once.Do(func() {
			t.mu.Lock()
			if t.credential == credential {
				t.credential = nil
			}
			credential.cancel()
			t.mu.Unlock()
		})
	}
	return credential.token, revoke, nil
}

func transferError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": message})
}

// authorizedLocked reports whether r carries the current pairing's bearer token from its paired peer.
func (t *transferReceiver) authorizedLocked(r *http.Request, peer string) bool {
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	credential := t.credential
	return bearer && credential != nil && credential.ctx.Err() == nil && credential.peer == peer && len(token) == 64 &&
		subtle.ConstantTimeCompare([]byte(token), []byte(credential.token)) == 1
}

// authorized checks a non-transfer request (such as the shared clipboard) against the same pairing.
func (t *transferReceiver) authorized(r *http.Request, peerAllowed func(string) bool) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || peerAllowed == nil || !peerAllowed(peer) || r.Header.Get("Origin") != "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.authorizedLocked(r, peer)
}

func (t *transferReceiver) serveHTTP(w http.ResponseWriter, r *http.Request, peerAllowed func(string) bool) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || peerAllowed == nil || !peerAllowed(peer) || r.Method != http.MethodPost || r.Header.Get("Origin") != "" {
		transferError(w, http.StatusForbidden, "Pair with this Mac before sending files.")
		return
	}
	t.mu.Lock()
	credential := t.credential
	if !t.authorizedLocked(r, peer) {
		t.mu.Unlock()
		transferError(w, http.StatusForbidden, "Pair with this Mac before sending files.")
		return
	}
	if t.busy {
		t.mu.Unlock()
		transferError(w, http.StatusConflict, "Another transfer is in progress.")
		return
	}
	t.busy = true
	t.activeDone = make(chan struct{})
	t.mu.Unlock()
	defer func() { t.mu.Lock(); t.busy = false; close(t.activeDone); t.activeDone = nil; t.mu.Unlock() }()
	if r.ContentLength < 0 {
		transferError(w, http.StatusLengthRequired, "The transfer size is required.")
		return
	}
	if r.ContentLength > maxTransferBytes {
		transferError(w, http.StatusRequestEntityTooLarge, "The transfer exceeds the 100 GiB limit.")
		return
	}
	encodedName := r.Header.Get("X-MacDow-Name")
	if len(encodedName) > 320 {
		transferError(w, http.StatusBadRequest, "Choose a file or folder with a valid name.")
		return
	}
	nameBytes, err := base64.RawURLEncoding.Strict().DecodeString(encodedName)
	name, kind := string(nameBytes), r.Header.Get("X-MacDow-Kind")
	if err != nil || !safeTransferName(name) || kind != "file" && kind != "folder" {
		transferError(w, http.StatusBadRequest, "Choose a file or folder with a valid name.")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopSession := context.AfterFunc(credential.ctx, cancel)
	defer stopSession()
	// Closing the request body interrupts a blocked network Read when pairing
	// ends, rather than leaving an incomplete file waiting for another packet.
	stopBody := context.AfterFunc(ctx, func() {
		// net/http body.Close can wait behind an idle network Read. Interrupt
		// this upload's TCP read first; the video uses another connection.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		_ = r.Body.Close()
	})
	defer stopBody()
	if credential.ctx.Err() != nil {
		cancel()
	}
	if err := secureTransferRoot(t.root); err != nil {
		transferError(w, http.StatusInternalServerError, "The Mac could not prepare Downloads/mac-lan.")
		return
	}
	stage, err := os.CreateTemp(t.root, ".macdow-transfer-*")
	if err != nil {
		transferError(w, http.StatusInternalServerError, "The Mac could not prepare the transfer.")
		return
	}
	stageName := stage.Name()
	defer os.Remove(stageName)
	defer stage.Close()
	hash := sha256.New()
	n, err := copyTransfer(ctx, io.MultiWriter(stage, hash), r.Body, r.ContentLength)
	if err != nil || n != r.ContentLength || stage.Sync() != nil {
		transferError(w, http.StatusBadRequest, "The transfer was interrupted or incomplete. Try again.")
		return
	}
	if err = stage.Close(); err != nil || ctx.Err() != nil {
		transferError(w, http.StatusBadRequest, "The transfer was interrupted or incomplete. Try again.")
		return
	}
	var finalName string
	commit := func(publish func() (string, error)) (string, error) {
		// Publication starts only while the same pairing still authorizes it.
		// A disconnect after this commit starts can leave a completed transfer;
		// it can never start a new commit using a revoked capability.
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.credential != credential || credential.ctx.Err() != nil || ctx.Err() != nil {
			return "", errTransfer
		}
		return publish()
	}
	if kind == "folder" {
		finalName, err = t.receiveFolder(ctx, stageName, name, n, commit)
	} else {
		finalName, err = commit(func() (string, error) { return t.publishFile(ctx, stageName, name) })
	}
	if err != nil {
		transferError(w, http.StatusBadRequest, "The Mac could not receive this file or folder. No existing files were replaced.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(transferReceipt{OK: true, Name: finalName, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: n})
}

func (t *transferReceiver) stopAndWait() {
	t.mu.Lock()
	t.stopped = true
	if t.credential != nil {
		t.credential.cancel()
		t.credential = nil
	}
	done := t.activeDone
	t.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func safeTransferName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 240 || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\:") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// Reject symlink redirection before creating any application-owned files.
func secureTransferRoot(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	remainder := strings.TrimPrefix(abs, volume)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(remainder, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if err = os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		st, statErr := os.Lstat(current)
		if statErr != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errTransfer
		}
	}
	return nil
}

// All reads and writes are bounded, including the extra byte used to reject a
// body whose declared length does not match the transmitted bytes.
func copyTransfer(ctx context.Context, destination io.Writer, source io.Reader, expected int64) (int64, error) {
	buffer := make([]byte, transferChunk)
	limited := io.LimitReader(source, expected+1)
	var written int64
	for {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		n, err := limited.Read(buffer)
		if n > 0 {
			if written+int64(n) > expected {
				return written, errTransfer
			}
			count, writeErr := destination.Write(buffer[:n])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != n {
				return written, io.ErrShortWrite
			}
		}
		if err != nil {
			if err == io.EOF && written == expected {
				return written, ctx.Err()
			}
			return written, err
		}
	}
}

func uniqueTransferName(original string, index int, folder bool) string {
	if index == 1 {
		return original
	}
	extension := ""
	stem := original
	if !folder {
		extension = filepath.Ext(original)
		stem = strings.TrimSuffix(original, extension)
	}
	suffix := fmt.Sprintf(" (%d)", index)
	// Leave room for the suffix even for a maximum-length UTF-8 name. Very
	// long extensions count as part of the name rather than defeating the cap.
	if len(extension) > 32 {
		stem, extension = original, ""
	}
	available := 240 - len(extension) - len(suffix)
	if len(stem) > available {
		stem = stem[:available]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
	}
	return stem + suffix + extension
}

func (t *transferReceiver) publishFile(ctx context.Context, stageName, name string) (string, error) {
	for index := 1; index <= 10000; index++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		candidate := uniqueTransferName(name, index, false)
		// Hard linking on the same filesystem is atomic and fails when the
		// destination exists; os.Rename would replace a user's existing file.
		err := os.Link(stageName, filepath.Join(t.root, candidate))
		if err == nil {
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", errTransfer
}

func safeZIPPath(name string, isDirectory bool) (string, bool) {
	if isDirectory {
		name = strings.TrimSuffix(name, "/")
	}
	if name == "" || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return "", false
	}
	parts := strings.Split(name, "/")
	if len(parts) > maxTransferDepth {
		return "", false
	}
	for _, part := range parts {
		if !safeTransferName(part) {
			return "", false
		}
	}
	return name, true
}

func (t *transferReceiver) receiveFolder(ctx context.Context, archive, name string, size int64, commit func(func() (string, error)) (string, error)) (string, error) {
	file, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := boundedZIPDirectory(file, size); err != nil {
		return "", err
	}
	z, err := zip.NewReader(file, size)
	if err != nil || len(z.File) > maxTransferEntries {
		return "", errTransfer
	}
	seen := make(map[string]bool, len(z.File))
	var total uint64
	for _, entry := range z.File {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		mode := entry.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return "", errTransfer
		}
		relative, valid := safeZIPPath(entry.Name, mode.IsDir())
		key := strings.ToLower(relative)
		if !valid || seen[key] || entry.UncompressedSize64 > uint64(maxTransferBytes)-total || mode.IsDir() && entry.UncompressedSize64 != 0 {
			return "", errTransfer
		}
		seen[key] = true
		total += entry.UncompressedSize64
	}
	stage, err := os.MkdirTemp(t.root, ".macdow-transfer-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	createdDirectories := map[string]bool{"": true}
	for _, entry := range z.File {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		relative, _ := safeZIPPath(entry.Name, entry.Mode().IsDir())
		directory := path.Dir(relative)
		if directory == "." {
			directory = ""
		}
		if entry.Mode().IsDir() {
			directory = relative
		}
		if err := createTransferDirectories(stage, directory, createdDirectories); err != nil {
			return "", err
		}
		if entry.Mode().IsDir() {
			continue
		}
		output, err := os.OpenFile(filepath.Join(stage, filepath.FromSlash(relative)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		input, err := entry.Open()
		if err != nil {
			output.Close()
			return "", err
		}
		_, copyErr := copyTransfer(ctx, output, input, int64(entry.UncompressedSize64))
		inputErr, outputErr := input.Close(), output.Close()
		if copyErr != nil || inputErr != nil || outputErr != nil {
			return "", errTransfer
		}
	}
	return commit(func() (string, error) {
		for index := 1; index <= 10000; index++ {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			candidate := uniqueTransferName(name, index, true)
			final := filepath.Join(t.root, candidate)
			// Reserve the destination exclusively. Move only fully validated staged
			// children into our own new directory, never replace an existing folder.
			if err := os.Mkdir(final, 0700); err != nil {
				if os.IsExist(err) {
					continue
				}
				return "", err
			}
			children, err := os.ReadDir(stage)
			if err != nil {
				_ = os.Remove(final)
				return "", err
			}
			for _, child := range children {
				if ctx.Err() != nil {
					_ = os.RemoveAll(final)
					return "", ctx.Err()
				}
				if err := os.Rename(filepath.Join(stage, child.Name()), filepath.Join(final, child.Name())); err != nil {
					_ = os.RemoveAll(final)
					return "", err
				}
			}
			return candidate, nil
		}
		return "", errTransfer
	})
}

// archive/zip allocates metadata while reading the entire central directory.
// Bound it before handing a potentially huge upload to zip.NewReader. ZIP64 is
// supported for large clips, but multi-disk archives are never accepted.
func boundedZIPDirectory(file *os.File, size int64) error {
	const maxDirectoryBytes = 16 << 20
	if size < 22 {
		return errTransfer
	}
	tailSize := int64(22 + 65535)
	if size < tailSize {
		tailSize = size
	}
	tail := make([]byte, int(tailSize))
	if _, err := file.ReadAt(tail, size-tailSize); err != nil {
		return err
	}
	for position := len(tail) - 22; position >= 0; position-- {
		entry := tail[position:]
		if binary.LittleEndian.Uint32(entry) != 0x06054b50 || position+22+int(binary.LittleEndian.Uint16(entry[20:])) != len(tail) {
			continue
		}
		if binary.LittleEndian.Uint16(entry[4:]) != 0 || binary.LittleEndian.Uint16(entry[6:]) != 0 {
			return errTransfer
		}
		countDisk, count := uint64(binary.LittleEndian.Uint16(entry[8:])), uint64(binary.LittleEndian.Uint16(entry[10:]))
		directorySize, offset := uint64(binary.LittleEndian.Uint32(entry[12:])), uint64(binary.LittleEndian.Uint32(entry[16:]))
		endOffset := uint64(size - tailSize + int64(position))
		if count == 0xffff || countDisk == 0xffff || directorySize == 0xffffffff || offset == 0xffffffff {
			if endOffset < 20 {
				return errTransfer
			}
			locator := make([]byte, 20)
			if _, err := file.ReadAt(locator, int64(endOffset)-20); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(locator) != 0x07064b50 || binary.LittleEndian.Uint32(locator[4:]) != 0 || binary.LittleEndian.Uint32(locator[16:]) != 1 {
				return errTransfer
			}
			zip64Offset := binary.LittleEndian.Uint64(locator[8:])
			if zip64Offset > endOffset-20 || endOffset-20-zip64Offset < 56 {
				return errTransfer
			}
			zip64 := make([]byte, 56)
			if _, err := file.ReadAt(zip64, int64(zip64Offset)); err != nil {
				return err
			}
			recordSize := binary.LittleEndian.Uint64(zip64[4:])
			if binary.LittleEndian.Uint32(zip64) != 0x06064b50 || recordSize < 44 || recordSize > endOffset-20-zip64Offset-12 || recordSize != endOffset-20-zip64Offset-12 || binary.LittleEndian.Uint32(zip64[16:]) != 0 || binary.LittleEndian.Uint32(zip64[20:]) != 0 {
				return errTransfer
			}
			countDisk, count = binary.LittleEndian.Uint64(zip64[24:]), binary.LittleEndian.Uint64(zip64[32:])
			directorySize, offset = binary.LittleEndian.Uint64(zip64[40:]), binary.LittleEndian.Uint64(zip64[48:])
			endOffset = zip64Offset
		}
		if countDisk != count || count > maxTransferEntries || directorySize > maxDirectoryBytes || offset > endOffset || directorySize != endOffset-offset {
			return errTransfer
		}
		// zip.NewReader does not trust the declared directory size as its own
		// read bound. Independently scan every actual central-directory header
		// and require exact byte coverage and count before it can allocate names.
		position := offset
		var actualCount uint64
		for position < endOffset {
			if endOffset-position < 46 || actualCount >= maxTransferEntries {
				return errTransfer
			}
			header := make([]byte, 46)
			if _, err := file.ReadAt(header, int64(position)); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(header) != 0x02014b50 || binary.LittleEndian.Uint16(header[34:]) != 0 {
				return errTransfer
			}
			length := uint64(46) + uint64(binary.LittleEndian.Uint16(header[28:])) + uint64(binary.LittleEndian.Uint16(header[30:])) + uint64(binary.LittleEndian.Uint16(header[32:]))
			if length > endOffset-position {
				return errTransfer
			}
			position += length
			actualCount++
		}
		if actualCount != count {
			return errTransfer
		}
		return nil
	}
	return errTransfer
}

func createTransferDirectories(root, relative string, created map[string]bool) error {
	if created[relative] {
		return nil
	}
	current := ""
	for _, part := range strings.Split(relative, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		if created[current] {
			continue
		}
		// An unexpected existing directory can be a case or Unicode-normalized
		// alias on a Mac volume. Reject it rather than merge distinct ZIP paths.
		if err := os.Mkdir(filepath.Join(root, filepath.FromSlash(current)), 0700); err != nil {
			return err
		}
		created[current] = true
	}
	return nil
}
