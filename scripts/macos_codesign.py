"""Code signing for mac-lan.app, written for Windows packaging.

macOS remembers Screen Recording and Accessibility grants by the app's
designated requirement. An ad-hoc signature's requirement is its exact code hash,
so every build looked like a new app. Signing with one persistent certificate
and an explicit requirement,

    identifier "com.tbhtho.macdow.host" and certificate leaf = H"<sha1 of cert>"

lets later builds satisfy grants made for earlier ones. The certificate is
self-made, not an Apple Developer ID: Gatekeeper still asks once, and nothing is
notarized. Without a key this falls back to the ad-hoc signature.

Go's linker gives each arm64 binary a "linker-signed" ad-hoc signature that
seals nothing; both binaries are re-signed and the bundle (Info.plist, resources,
nested helper) is sealed the way `codesign` does.

Usage: python -I macos_codesign.py keygen <cert.cer> <key.pem>
       python -I macos_codesign.py sign-app <mac-lan.app> <cert.cer> <key.pem>
"""
import datetime, hashlib, plistlib, secrets, struct, sys

PAGE = 4096
CS_ADHOC = 0x2
EXECSEG_MAIN_BINARY = 0x1
MAIN = 'Contents/MacOS/mac-lan'
HELPER = 'Contents/Helpers/MacDowHost'
EMPTY_REQUIREMENTS = struct.pack('>III', 0xFADE0C01, 12, 0)


# --- minimal DER -----------------------------------------------------------
def _tlv(tag, content):
    n = len(content)
    if n < 0x80: length = bytes([n])
    else:
        raw = n.to_bytes((n.bit_length() + 7) // 8, 'big'); length = bytes([0x80 | len(raw)]) + raw
    return bytes([tag]) + length + content
def _seq(*items): return _tlv(0x30, b''.join(items))
def _set(*items): return _tlv(0x31, b''.join(sorted(items)))
def _int(v): return _tlv(0x02, v.to_bytes(v.bit_length() // 8 + 1, 'big', signed=False) if v else b'\0')
def _octets(b): return _tlv(0x04, b)
def _oid(text):
    parts = [int(p) for p in text.split('.')]
    out = bytes([40 * parts[0] + parts[1]])
    for p in parts[2:]:
        chunk = [p & 0x7F]; p >>= 7
        while p: chunk.append(0x80 | (p & 0x7F)); p >>= 7
        out += bytes(reversed(chunk))
    return _tlv(0x06, out)
SHA256 = '2.16.840.1.101.3.4.2.1'


# --- signing identity ------------------------------------------------------
def keygen(cert_path, key_path):
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes, serialization
    from cryptography.hazmat.primitives.asymmetric import rsa
    from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID
    from pathlib import Path
    for path in (cert_path, key_path):
        if Path(path).exists(): raise SystemExit(f'{path} exists; never replace the signing identity (grants depend on it)')
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, 'mac-lan code signing'), x509.NameAttribute(NameOID.ORGANIZATION_NAME, 'tbhtho')])
    now = datetime.datetime.now(datetime.timezone.utc)
    cert = (x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key())
            .serial_number(x509.random_serial_number()).not_valid_before(now - datetime.timedelta(days=1))
            .not_valid_after(now + datetime.timedelta(days=365 * 30))
            .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
            .add_extension(x509.KeyUsage(True, False, False, False, False, False, False, False, False), critical=True)
            .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.CODE_SIGNING]), critical=True)
            .add_extension(x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False)
            .sign(key, hashes.SHA256()))
    Path(key_path).parent.mkdir(parents=True, exist_ok=True)
    with open(key_path, 'xb') as f:
        f.write(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    Path(cert_path).write_bytes(cert.public_bytes(serialization.Encoding.DER))
    print('Created mac-lan code signing certificate', hashlib.sha1(cert.public_bytes(serialization.Encoding.DER)).hexdigest())


def load_identity(cert_path, key_path):
    from cryptography.hazmat.primitives import serialization
    from pathlib import Path
    from cryptography import x509
    cert = Path(cert_path).read_bytes()
    key = serialization.load_pem_private_key(Path(key_path).read_bytes(), password=None)
    if key.public_key().public_numbers() != x509.load_der_x509_certificate(cert).public_key().public_numbers():
        raise SystemExit('Signing key does not match the committed certificate')
    return cert, key


def _data(b): return struct.pack('>I', len(b)) + b + b'\0' * ((-len(b)) % 4)


def designated_requirement(identifier, cert):
    """identifier "<id>" and certificate leaf = H"<sha1>" as a compiled requirement set."""
    expr = struct.pack('>II', 6, 2) + _data(identifier.encode()) + struct.pack('>Ii', 4, 0) + _data(hashlib.sha1(cert).digest())
    requirement = struct.pack('>III', 0xFADE0C00, 12 + len(expr), 1) + expr
    return struct.pack('>IIIII', 0xFADE0C01, 20 + len(requirement), 1, 3, 20) + requirement


def requirement_text(identifier, cert):
    return 'identifier "%s" and certificate leaf = H"%s"' % (identifier, hashlib.sha1(cert).hexdigest())


def _cms(code_directory, cert, key, when):
    """Detached CMS SignedData over the CodeDirectory, with Apple's cdhash attributes."""
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes
    from cryptography.hazmat.primitives.asymmetric import padding
    parsed = x509.load_der_x509_certificate(cert)
    digest = hashlib.sha256(code_directory).digest()
    cdhashes = plistlib.dumps({'cdhashes': [digest[:20]]}, fmt=plistlib.FMT_XML)
    attributes = [
        _seq(_oid('1.2.840.113549.1.9.3'), _set(_oid('1.2.840.113549.1.7.1'))),
        _seq(_oid('1.2.840.113549.1.9.5'), _set(_tlv(0x17, when.strftime('%y%m%d%H%M%SZ').encode()))),
        _seq(_oid('1.2.840.113549.1.9.4'), _set(_octets(digest))),
        _seq(_oid('1.2.840.113635.100.9.1'), _set(_octets(cdhashes))),
        _seq(_oid('1.2.840.113635.100.9.2'), _set(_seq(_oid(SHA256), _octets(digest)))),
    ]
    signed = _set(*attributes)
    signature = key.sign(signed, padding.PKCS1v15(), hashes.SHA256())
    signer = _seq(_int(1), _seq(parsed.issuer.public_bytes(), _int(parsed.serial_number)), _seq(_oid(SHA256)),
                  b'\xa0' + signed[1:], _seq(_oid('1.2.840.113549.1.1.1'), b'\x05\x00'), _octets(signature))
    body = _seq(_int(1), _set(_seq(_oid(SHA256))), _seq(_oid('1.2.840.113549.1.7.1')), _tlv(0xA0, cert), _set(signer))
    return _seq(_oid('1.2.840.113549.1.7.2'), _tlv(0xA0, body))


# --- Mach-O ----------------------------------------------------------------
def _load_commands(binary):
    magic, _, _, _, ncmds = struct.unpack('<IiiII', binary[:20])
    if magic != 0xFEEDFACF: raise ValueError('not a 64-bit Mach-O')
    offset = 32
    for _ in range(ncmds):
        cmd, size = struct.unpack('<II', binary[offset:offset + 8])
        yield cmd, offset
        offset += size


def _layout(data):
    signature = linkedit = text = None
    for cmd, offset in _load_commands(data):
        if cmd == 0x1D: signature = offset
        elif cmd == 0x19:
            name = bytes(data[offset + 8:offset + 24]).rstrip(b'\0')
            if name == b'__LINKEDIT': linkedit = offset
            elif name == b'__TEXT': text = offset
    if signature is None or linkedit is None or text is None: raise ValueError('Mach-O lacks a signature slot')
    return signature, linkedit, text


def code_bytes(binary):
    """The binary with its signature removed and signature-size fields cleared, for comparing builds."""
    data = bytearray(binary)
    signature, linkedit, _ = _layout(data)
    limit = struct.unpack('<I', data[signature + 8:signature + 12])[0]
    del data[limit:]
    struct.pack_into('<I', data, signature + 12, 0)
    struct.pack_into('<QQ', data, linkedit + 32, 0, 0)
    struct.pack_into('<Q', data, linkedit + 48, 0)
    return bytes(data)


def sign_macho(binary, identifier, special_hashes, identity=None, when=None):
    """Return (signed binary, cdhash). special_hashes maps slot -> sha256; slot 2 is filled here."""
    data = bytearray(binary)
    signature, linkedit, text = _layout(data)
    code_limit = struct.unpack('<I', data[signature + 8:signature + 12])[0]
    del data[code_limit:]
    requirements = designated_requirement(identifier, identity[0]) if identity else EMPTY_REQUIREMENTS
    special = dict(special_hashes); special[2] = hashlib.sha256(requirements).digest()
    ident = identifier.encode() + b'\0'
    slots, pages = max(special), (code_limit + PAGE - 1) // PAGE
    cd_length = 88 + len(ident) + 32 * (slots + pages)
    when = when or datetime.datetime.now(datetime.timezone.utc)
    # The CMS length does not depend on the CodeDirectory's contents.
    cms_length = len(_cms(b'\0' * cd_length, identity[0], identity[1], when)) + 8 if identity else 8
    total = 12 + 8 * 3 + cd_length + len(requirements) + cms_length
    size = (total + 15) // 16 * 16
    struct.pack_into('<I', data, signature + 12, size)
    fileoff = struct.unpack('<Q', data[linkedit + 40:linkedit + 48])[0]
    filesize = code_limit + size - fileoff
    struct.pack_into('<Q', data, linkedit + 48, filesize)
    struct.pack_into('<Q', data, linkedit + 32, (filesize + 0x3FFF) // 0x4000 * 0x4000)
    text_off, text_size = struct.unpack('<QQ', data[text + 40:text + 56])
    flags = 0 if identity else CS_ADHOC
    cd = struct.pack('>IIIIIIIIIBBBBIIIIQQQQ', 0xFADE0C02, cd_length, 0x20400, flags, 88 + len(ident) + 32 * slots, 88, slots, pages,
                     code_limit, 32, 2, 0, 12, 0, 0, 0, 0, 0, text_off, text_size, EXECSEG_MAIN_BINARY)
    cd += ident
    for slot in range(slots, 0, -1): cd += special.get(slot, b'\0' * 32)
    for page in range(pages): cd += hashlib.sha256(data[page * PAGE:min((page + 1) * PAGE, code_limit)]).digest()
    assert len(cd) == cd_length
    cms = struct.pack('>II', 0xFADE0B01, cms_length) + (_cms(cd, identity[0], identity[1], when) if identity else b'')
    assert len(cms) == cms_length
    entries = [(0, cd), (2, requirements), (0x10000, cms)]
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


def sign_bundle(files, bundle_identifier, identity=None):
    """files: {'Contents/...': (mode, bytes or None for a directory)}. Returns the signed copy."""
    signed = {k: v for k, v in files.items() if not k.startswith('Contents/_CodeSignature')}
    when = datetime.datetime.now(datetime.timezone.utc)
    helper_id = bundle_identifier + '.helper'
    helper, helper_cdhash = sign_macho(files[HELPER][1], helper_id, {}, identity, when)
    signed[HELPER] = (files[HELPER][0], helper)
    sealed_v1, sealed_v2 = {}, {}
    for path, (mode, data) in sorted(signed.items()):
        if data is None or path in (MAIN, HELPER, 'Contents/Info.plist'): continue
        rel = path[len('Contents/'):]
        if not rel.startswith('Resources/'): raise ValueError('unsealed bundle content: ' + path)
        sealed_v1[rel] = hashlib.sha1(data).digest()
        sealed_v2[rel] = {'hash2': hashlib.sha256(data).digest()}
    requirement = requirement_text(helper_id, identity[0]) if identity else 'cdhash H"%s"' % helper_cdhash.hex()
    sealed_v2['Helpers/MacDowHost'] = {'cdhash': helper_cdhash, 'requirement': requirement}
    rules = _rules()
    resources = plistlib.dumps({'files': sealed_v1, 'files2': sealed_v2, 'rules': rules['files'], 'rules2': rules['files2']},
                               fmt=plistlib.FMT_XML, sort_keys=True)
    special = {1: hashlib.sha256(files['Contents/Info.plist'][1]).digest(), 3: hashlib.sha256(resources).digest()}
    main, _ = sign_macho(files[MAIN][1], bundle_identifier, special, identity, when)
    signed[MAIN] = (files[MAIN][0], main)
    signed['Contents/_CodeSignature'] = (0o40755, None)
    signed['Contents/_CodeSignature/CodeResources'] = (0o100644, resources)
    return signed


def verify_macho(binary, identifier, special_hashes, cert=None):
    """Independent re-parse: every page hash, the bound slots, the requirement and the CMS signature."""
    for cmd, offset in _load_commands(binary):
        if cmd == 0x1D: dataoff, datasize = struct.unpack('<II', binary[offset + 8:offset + 16])
    blob = binary[dataoff:dataoff + datasize]
    magic, length, count = struct.unpack('>III', blob[:12])
    assert magic == 0xFADE0CC0 and dataoff + length <= len(binary)
    blobs = {}
    for i in range(count):
        kind, off = struct.unpack('>II', blob[12 + 8 * i:20 + 8 * i])
        blobs[kind] = blob[off:off + struct.unpack('>I', blob[off + 4:off + 8])[0]]
    cd = blobs[0]
    _, _, version, flags, hash_offset, ident_offset, slots, pages, limit = struct.unpack('>IIIIIIIII', cd[:36])
    assert flags == (0 if cert else CS_ADHOC) and limit == dataoff and pages == (limit + PAGE - 1) // PAGE
    assert cd[ident_offset:].split(b'\0')[0].decode() == identifier
    expected = dict(special_hashes); expected[2] = hashlib.sha256(blobs[2]).digest()
    for slot, digest in expected.items():
        assert cd[hash_offset - 32 * slot:hash_offset - 32 * slot + 32] == digest, 'special slot %d' % slot
    for page in range(pages):
        assert cd[hash_offset + 32 * page:hash_offset + 32 * page + 32] == hashlib.sha256(binary[page * PAGE:min((page + 1) * PAGE, limit)]).digest()
    if cert:
        assert blobs[2] == designated_requirement(identifier, cert), 'designated requirement'
        _verify_cms(blobs[0x10000][8:], cd, cert)
    return hashlib.sha256(cd).digest()[:20]


def _verify_cms(cms, cd, cert):
    """Check the CMS with the certificate's public key, using cryptography's own DER parsing of the cert."""
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes
    from cryptography.hazmat.primitives.asymmetric import padding
    def read(b, i):
        tag, n = b[i], b[i + 1]; i += 2
        if n & 0x80: k = n & 0x7F; n = int.from_bytes(b[i:i + k], 'big'); i += k
        return tag, b[i:i + n], i + n
    def children(b):
        out, i = [], 0
        while i < len(b): t, c, i2 = read(b, i); out.append((t, c, b[i:i2])); i = i2
        return out
    _, info, _ = read(cms, 0)
    signed_data = children(children(info)[1][1])[0][1]
    parts = children(signed_data)
    assert any(t == 0xA0 and c == cert for t, c, _ in parts), 'certificate not embedded'
    signer = children([c for t, c, _ in parts if t == 0x31][-1])[0][1]
    fields = children(signer)
    attrs = next(raw for t, _, raw in fields if t == 0xA0)
    signature = fields[-1][1]
    assert hashlib.sha256(cd).digest() in attrs, 'messageDigest does not cover the CodeDirectory'
    x509.load_der_x509_certificate(cert).public_key().verify(signature, b'\x31' + attrs[1:], padding.PKCS1v15(), hashes.SHA256())


def sign_app_directory(bundle, cert_path, key_path):
    """Sign an assembled mac-lan.app folder in place; ad-hoc when the key file is absent."""
    from pathlib import Path
    root = Path(bundle)
    identity = load_identity(cert_path, key_path) if Path(key_path).exists() else None
    files = {}
    for path in sorted(root.rglob('*')):
        rel = path.relative_to(root).as_posix()
        files[rel] = (0o40755, None) if path.is_dir() else (0o100644, path.read_bytes())
    identifier = plistlib.loads(files['Contents/Info.plist'][1])['CFBundleIdentifier']
    signed = sign_bundle(files, identifier, identity)
    for rel, (_, data) in signed.items():
        target = root / rel
        if data is None: target.mkdir(exist_ok=True)
        else: target.write_bytes(data)
    cert = identity[0] if identity else None
    resources = signed['Contents/_CodeSignature/CodeResources'][1]
    verify_macho(signed[HELPER][1], identifier + '.helper', {}, cert)
    verify_macho(signed[MAIN][1], identifier, {1: hashlib.sha256(signed['Contents/Info.plist'][1]).digest(), 3: hashlib.sha256(resources).digest()}, cert)
    kind = 'certificate ' + hashlib.sha1(cert).hexdigest() if cert else 'ad-hoc (no signing key)'
    print('Signed and verified mac-lan.app:', kind)
    return 'certificate' if cert else 'ad-hoc'


if __name__ == '__main__':
    if len(sys.argv) == 4 and sys.argv[1] == 'keygen': keygen(sys.argv[2], sys.argv[3])
    elif len(sys.argv) == 5 and sys.argv[1] == 'sign-app': sign_app_directory(sys.argv[2], sys.argv[3], sys.argv[4])
    else: raise SystemExit('usage: macos_codesign.py keygen <cert> <key> | sign-app <bundle> <cert> <key>')
