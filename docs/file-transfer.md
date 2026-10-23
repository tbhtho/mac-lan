# Windows to Mac transfers

Pair the Windows app with the Mac, then choose **Files** in the Windows tab bar.
Select files, ZIPs, or a folder, or drag them into the transfer window. The Mac
receives them in `~/Downloads/MAC-DOW`. A folder arrives as a folder, and a ZIP
selected as a file stays a ZIP. Existing names receive a numbered suffix; nothing
is overwritten. The Mac's pairing approval also authorizes receiving files for
that pairing. Video requires its own approval. File transfers remain available
when video cannot start.

The upload uses its own HTTPS request so that bulk file data does not enter the
video or keyboard/mouse WebSocket queues. Windows pins the same ephemeral TLS
certificate used for pairing. Uploads use bounded buffers; both connections share
the LAN's bandwidth. Sustained video performance during a bulk upload remains
unmeasured.

## Wire contract

The successful `/session` catalog includes an unpredictable `transferToken` of
64 lowercase hexadecimal characters. It is bound to the approved connection's
peer IPv4 address and revoked when that WebSocket ends. A single transfer may run
at a time. Closing either app or disconnecting cancels incomplete transfers.
Publication is serialized with revocation: a fully received transfer whose final
commit has already begun can finish even if the connection closes at that point.

Send a separate `POST /transfer` on the same TLS listener with:

- `Authorization: Bearer <transferToken>`
- `X-MacDow-Name`: the UTF-8 destination basename encoded using unpadded base64url
- `X-MacDow-Kind`: `file` or `folder`
- A known `Content-Length` from 0 through 100 GiB

The server rejects browser `Origin` headers, non-LAN or different-peer requests,
unpaired or revoked capabilities, overlapping uploads, and invalid names. Names
must be valid UTF-8 and at most 240 bytes, excluding `.`, `..`, path separators,
colons, and control characters. Empty files are supported.

For `file`, the body is the original bytes. For `folder`, the body is a standard
single-disk ZIP or ZIP64 archive with relative `/` entry paths and no outer folder
prefix. Include empty directory entries. The receiver rejects absolute paths,
traversal, backslashes, colons, duplicate or conflicting paths, symlinks and other
special files, corrupt payloads, and size inconsistencies. Directory aliases on
case-insensitive or Unicode-normalizing volumes are rejected rather than merged.
Limits are 10,000 ZIP entries, 32 path components, 16 MiB of central-directory
metadata, and 100 GiB of total uncompressed data. Both archive and expanded data
are staged on disk; Windows needs temporary space for the folder ZIP and the Mac
needs space for the ZIP and extracted folder during receipt.

All network and extraction copies use buffers of at most 256 KiB. Incomplete
application-owned staging data is removed on cancellation or failure. The
destination directory is checked against symlink redirection. Final files are
published through an exclusive hard link; final folders reserve a new directory
exclusively before moving validated staged contents into it.

A successful response is HTTP 200 with JSON:

```json
{"ok":true,"name":"Clips (2)","sha256":"<64 lowercase hexadecimal characters>","bytes":12345}
```

`name` is the unique destination basename. `sha256` and `bytes` describe exactly
the transmitted body (the ZIP bytes for a folder). Windows verifies both values
before showing success. Failure responses contain `ok: false` and a generic
message; they contain no user path, token, filename, or file contents. Names,
paths, tokens, and transferred data are never sent through diagnostics.
