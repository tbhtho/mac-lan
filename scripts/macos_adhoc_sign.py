"""Ad-hoc code signing for the mac-lan.app bundle, written for Windows packaging.

Go's linker gives each arm64 binary a minimal "linker-signed" ad-hoc signature
that seals nothing else. macOS reports a downloaded bundle like that as damaged.
This rewrites both binaries with a regular ad-hoc signature and seals the bundle
(Info.plist, resources and the nested helper) the way `codesign -s -` does, so
Gatekeeper treats it as an unidentified-developer app instead. It is not a
Developer ID signature and does not notarize anything.

Bundle content is a dict of {path inside the .app: (mode, bytes)}; signing returns
a new dict with signed binaries and Contents/_CodeSignature/CodeResources added.
"""
import hashlib, plistlib, struct

PAGE = 4096
CS_ADHOC = 0x2
EXECSEG_MAIN_BINARY = 0x1
REQUIREMENTS = struct.pack('>III', 0xFADE0C01, 12, 0)          # empty requirement set
EMPTY_CMS = struct.pack('>II', 0xFADE0B01, 8)                   # ad-hoc: no CMS signature
MAIN = 'Contents/MacOS/mac-lan'
HELPER = 'Contents/Helpers/MacDowHost'


def _load_commands(binary):
    magic, _, _, _, ncmds = struct.unpack('<IiiII', binary[:20])
    if magic != 0xFEEDFACF: raise ValueError('not a 64-bit Mach-O')
    offset = 32
    for _ in range(ncmds):
        cmd, size = struct.unpack('<II', binary[offset:offset + 8])
        yield cmd, offset
        offset += size


def sign_macho(binary, identifier, special_hashes):
    """Return binary with a fresh ad-hoc signature. special_hashes maps slot -> sha256 digest."""
    data = bytearray(binary)
    signature = linkedit = text = None
    for cmd, offset in _load_commands(data):
        if cmd == 0x1D: signature = offset
        elif cmd == 0x19:
            name = bytes(data[offset + 8:offset + 24]).rstrip(b'\0')
            if name == b'__LINKEDIT': linkedit = offset
            elif name == b'__TEXT': text = offset
    if signature is None or linkedit is None or text is None: raise ValueError('Mach-O lacks a signature slot')
    code_limit = struct.unpack('<I', data[signature + 8:signature + 12])[0]
    del data[code_limit:]
    ident = identifier.encode() + b'\0'
    slots = max(special_hashes, default=0)
    pages = (code_limit + PAGE - 1) // PAGE
    cd_length = 88 + len(ident) + 32 * (slots + pages)
    blobs = 3
    total = 12 + 8 * blobs + cd_length + len(REQUIREMENTS) + len(EMPTY_CMS)
    size = (total + 15) // 16 * 16
    # Point the load commands at the new signature before hashing the header page.
    struct.pack_into('<I', data, signature + 12, size)
    fileoff = struct.unpack('<Q', data[linkedit + 40:linkedit + 48])[0]
    filesize = code_limit + size - fileoff
    struct.pack_into('<Q', data, linkedit + 48, filesize)
    struct.pack_into('<Q', data, linkedit + 32, (filesize + 0x3FFF) // 0x4000 * 0x4000)
    text_off, text_size = struct.unpack('<QQ', data[text + 40:text + 56])
    hash_offset = 88 + len(ident) + 32 * slots
    cd = struct.pack('>IIIIIIIIIBBBBIIIIQQQQ', 0xFADE0C02, cd_length, 0x20400, CS_ADHOC, hash_offset, 88, slots, pages,
                     code_limit, 32, 2, 0, 12, 0, 0, 0, 0, 0, text_off, text_size, EXECSEG_MAIN_BINARY)
    cd += ident
    for slot in range(slots, 0, -1):
        cd += special_hashes.get(slot, b'\0' * 32)
    for page in range(pages):
        cd += hashlib.sha256(data[page * PAGE:min((page + 1) * PAGE, code_limit)]).digest()
    assert len(cd) == cd_length
    entries = [(0, cd), (2, REQUIREMENTS), (0x10000, EMPTY_CMS)]
    offset = 12 + 8 * len(entries)
    index = b''
    for kind, blob in entries:
        index += struct.pack('>II', kind, offset); offset += len(blob)
    superblob = struct.pack('>III', 0xFADE0CC0, offset, len(entries)) + index + b''.join(b for _, b in entries)
    data += superblob + b'\0' * (size - len(superblob))
    return bytes(data), hashlib.sha256(cd).digest()[:20]


def _rules():
    return {
        'files': {'^Resources/': True, '^Resources/.*\\.lproj/': {'optional': True, 'weight': 1000.0},
                  '^Resources/.*\\.lproj/locversion.plist$': {'omit': True, 'weight': 1100.0},
                  '^Resources/Base\\.lproj/': {'weight': 1010.0}, '^version.plist$': True},
        'files2': {'.*\\.dSYM($|/)': {'weight': 11.0}, '^(.*/)?\\.DS_Store$': {'omit': True, 'weight': 2000.0},
                   '^(Frameworks|SharedFrameworks|PlugIns|Plug-ins|XPCServices|Helpers|MacOS|Library/(Automator|Spotlight|LoginItems))/': {'nested': True, 'weight': 10.0},
                   '^.*': True, '^Info\\.plist$': {'omit': True, 'weight': 20.0}, '^PkgInfo$': {'omit': True, 'weight': 20.0},
                   '^Resources/': {'weight': 20.0}, '^Resources/.*\\.lproj/': {'optional': True, 'weight': 1000.0},
                   '^Resources/.*\\.lproj/locversion.plist$': {'omit': True, 'weight': 1100.0},
                   '^Resources/Base\\.lproj/': {'weight': 1010.0}, '^[^/]+$': {'nested': True, 'weight': 10.0},
                   '^embedded\\.provisionprofile$': {'weight': 20.0}, '^version\\.plist$': {'weight': 20.0}},
    }


def sign_bundle(files, bundle_identifier):
    """files: {'Contents/...': (mode, bytes)} relative to the .app. Returns the signed copy."""
    signed = dict(files)
    helper, helper_cdhash = sign_macho(files[HELPER][1], 'MacDowHost', {2: hashlib.sha256(REQUIREMENTS).digest()})
    signed[HELPER] = (files[HELPER][0], helper)
    sealed_v1, sealed_v2 = {}, {}
    for path, (mode, data) in sorted(files.items()):
        rel = path[len('Contents/'):]
        if data is None or path in (MAIN, HELPER, 'Contents/Info.plist'): continue
        if not rel.startswith('Resources/'): raise ValueError('unsealed bundle content: ' + path)
        sealed_v1[rel] = hashlib.sha1(data).digest()
        sealed_v2[rel] = {'hash2': hashlib.sha256(data).digest()}
    sealed_v2['Helpers/MacDowHost'] = {'cdhash': helper_cdhash, 'requirement': 'cdhash H"%s"' % helper_cdhash.hex()}
    rules = _rules()
    resources = plistlib.dumps({'files': sealed_v1, 'files2': sealed_v2, 'rules': rules['files'], 'rules2': rules['files2']},
                               fmt=plistlib.FMT_XML, sort_keys=True)
    special = {1: hashlib.sha256(files['Contents/Info.plist'][1]).digest(), 2: hashlib.sha256(REQUIREMENTS).digest(),
               3: hashlib.sha256(resources).digest()}
    main, _ = sign_macho(files[MAIN][1], bundle_identifier, special)
    signed[MAIN] = (files[MAIN][0], main)
    signed['Contents/_CodeSignature'] = (0o40755, None)
    signed['Contents/_CodeSignature/CodeResources'] = (0o100644, resources)
    return signed


def verify_macho(binary, identifier, special_hashes):
    """Independent re-parse: the signature covers every page and binds the special slots."""
    for cmd, offset in _load_commands(binary):
        if cmd == 0x1D: dataoff, datasize = struct.unpack('<II', binary[offset + 8:offset + 16])
    blob = binary[dataoff:dataoff + datasize]
    magic, length, count = struct.unpack('>III', blob[:12])
    assert magic == 0xFADE0CC0 and dataoff + length <= len(binary)
    for i in range(count):
        kind, off = struct.unpack('>II', blob[12 + 8 * i:20 + 8 * i])
        if kind == 0: cd = blob[off:off + struct.unpack('>I', blob[off + 4:off + 8])[0]]
    _, _, version, flags, hash_offset, ident_offset, slots, pages, limit = struct.unpack('>IIIIIIIII', cd[:36])
    assert flags == CS_ADHOC and limit == dataoff and pages == (limit + PAGE - 1) // PAGE
    assert cd[ident_offset:].split(b'\0')[0].decode() == identifier
    for slot, digest in special_hashes.items():
        assert cd[hash_offset - 32 * slot:hash_offset - 32 * slot + 32] == digest, 'special slot %d' % slot
    for page in range(pages):
        assert cd[hash_offset + 32 * page:hash_offset + 32 * page + 32] == hashlib.sha256(binary[page * PAGE:min((page + 1) * PAGE, limit)]).digest()
    return hashlib.sha256(cd).digest()[:20]
