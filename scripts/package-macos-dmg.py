"""Turn the verified mac-lan.app archive into a drag-to-Applications disk image.

No Apple tools run on Windows, so this writes a read-only ISO 9660 image with
Rock Ridge extensions, which macOS mounts from a .dmg like any other disk image.
Rock Ridge keeps the real names, executable modes and the Applications link.
The app comes unchanged from the verified, already signed archive; its
signature is re-checked here. Finder window settings and a hidden background
come from macos_dsstore. The image is read back and compared before the script
reports success.
"""
from pathlib import Path
import hashlib, plistlib, struct, sys, tarfile, time

sys.path.insert(0, str(Path(__file__).resolve().parent))
import macos_codesign, macos_dsstore  # noqa: E402  (sibling build modules)

SECTOR = 2048
VOLUME = b'MAC-LAN'
ER_ID = b'RRIP_1991A'
ER_DESC = b'THE ROCK RIDGE INTERCHANGE PROTOCOL PROVIDES SUPPORT FOR POSIX FILE SYSTEM SEMANTICS'
ER_SRC = b'PLEASE CONTACT DISC PUBLISHER FOR SPECIFICATION SOURCE.  SEE PUBLISHER IDENTIFIER IN PRIMARY VOLUME DESCRIPTOR FOR CONTACT INFORMATION.'


def both16(v): return struct.pack('<H', v) + struct.pack('>H', v)
def both32(v): return struct.pack('<I', v) + struct.pack('>I', v)
def text(value, size): return value.ljust(size, b' ')[:size]
def sectors(size): return (size + SECTOR - 1) // SECTOR


class Node:
    def __init__(self, name, kind, mode, data=b'', target=None):
        self.name, self.kind, self.mode, self.data, self.target = name, kind, mode, data, target
        self.children, self.iso, self.lba, self.size, self.parent, self.number = [], b'', 0, 0, None, 0


def tree(entries):
    """entries: {path: (mode, bytes | None for a directory | str link target)}."""
    root = Node('', 'dir', 0o40755); root.path = ''
    index = {'': root}
    def directory(path):
        if path not in index:
            parent = directory(path.rpartition('/')[0])
            node = Node(path.rpartition('/')[2], 'dir', 0o40755); node.path = path
            node.parent = parent; parent.children.append(node); index[path] = node
        return index[path]
    for path, (mode, data) in sorted(entries.items()):
        if data is None:
            directory(path).mode = mode
            continue
        parent = directory(path.rpartition('/')[0])
        node = Node(path.rpartition('/')[2], 'link', mode, target=data) if isinstance(data, str) else Node(path.rpartition('/')[2], 'file', mode, data)
        node.path = path; node.parent = parent; parent.children.append(node)
    return root


def archive_entries(archive_path):
    entries = {}
    with tarfile.open(archive_path, 'r:gz') as archive:
        for member in archive.getmembers():
            name = member.name.rstrip('/')
            if member.isdir(): entries[name] = (0o40000 | member.mode & 0o7777, None)
            elif member.isfile(): entries[name] = (0o100000 | member.mode & 0o7777, archive.extractfile(member).read())
            else: raise SystemExit('Unexpected archive member type: ' + member.name)
    return entries


def walk(node):
    yield node
    for child in node.children:
        if child.kind == 'dir': yield from walk(child)


def susp(node, entry_kind, ce_lba=0, er=b''):
    """System use entries: SP/CE on the root's '.', PX everywhere, NM for names, SL for links."""
    links = 2 + sum(c.kind == 'dir' for c in node.children) if node.kind == 'dir' else 1
    data = b''
    if entry_kind == 'root-self':
        data += b'SP' + bytes([7, 1, 0xBE, 0xEF, 0])
    data += b'PX' + bytes([36, 1]) + both32(node.mode) + both32(links) + both32(0) + both32(0)
    if entry_kind == 'root-self':
        data += b'CE' + bytes([28, 1]) + both32(ce_lba) + both32(0) + both32(len(er))
    if entry_kind == 'named':
        name = node.name.encode('utf-8')
        data += b'NM' + bytes([5 + len(name), 1, 0]) + name
        if node.kind == 'link':
            parts = node.target.split('/')
            components = bytes([0x08, 0]) if node.target.startswith('/') else b''
            for part in (p for p in parts if p):
                components += bytes([0, len(part)]) + part.encode('utf-8')
            data += b'SL' + bytes([5 + len(components), 1, 0]) + components
    return data


def record(lba, size, directory, ident, system_use, stamp):
    pad = b'\0' if len(ident) % 2 == 0 else b''
    if (33 + len(ident) + len(pad) + len(system_use)) % 2: system_use += b'\0'
    length = 33 + len(ident) + len(pad) + len(system_use)
    if length > 255: raise SystemExit('Directory record too long for ' + repr(ident))
    return (bytes([length, 0]) + both32(lba) + both32(size) + stamp + bytes([2 if directory else 0, 0, 0])
            + both16(1) + bytes([len(ident)]) + ident + pad + system_use)


def pack(records):
    """Records never cross a sector boundary."""
    out = b''
    for item in records:
        if len(out) // SECTOR != (len(out) + len(item) - 1) // SECTOR:
            out += b'\0' * (SECTOR - len(out) % SECTOR)
        out += item
    return out + b'\0' * ((-len(out)) % SECTOR)


def build(root, output, now):
    tm = time.gmtime(now)
    stamp = bytes([tm.tm_year - 1900, tm.tm_mon, tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec, 0])
    voldate = time.strftime('%Y%m%d%H%M%S', tm).encode() + b'00\0'
    er = b'ER' + bytes([8 + len(ER_ID) + len(ER_DESC) + len(ER_SRC), 1, len(ER_ID), len(ER_DESC), len(ER_SRC), 1]) + ER_ID + ER_DESC + ER_SRC
    counter = 0
    for directory in walk(root):
        directory.children.sort(key=lambda c: c.name)
        for child in directory.children:
            counter += 1
            child.iso = (b'D%06d' % counter) if child.kind == 'dir' else (b'F%06d.;1' % counter)
        directory.children.sort(key=lambda c: c.iso)
    # Path tables need breadth-first order: by level, then parent number, then identifier.
    directories = [root]
    for directory in directories: directories.extend(c for c in directory.children if c.kind == 'dir')
    for number, directory in enumerate(directories, 1): directory.number = number

    def entries(d):
        parent = d.parent or d
        yield record(d.lba, d.size, True, b'\0', susp(d, 'root-self' if d is root else 'self', ce_lba, er), stamp)
        yield record(parent.lba, parent.size, True, b'\1', susp(parent, 'parent'), stamp)
        for child in d.children:
            yield record(child.lba, child.size, child.kind == 'dir', child.iso, susp(child, 'named'), stamp)

    ce_lba = 0
    for d in directories: d.size = len(pack(list(entries(d))))   # sizes do not depend on extents
    def table(big):
        out = b''
        for d in directories:
            ident = b'\0' if d is root else d.iso
            out += bytes([len(ident), 0]) + struct.pack('>I' if big else '<I', d.lba) + struct.pack('>H' if big else '<H', (d.parent or d).number) + ident + (b'\0' if len(ident) % 2 else b'')
        return out
    table_size = len(table(False))
    next_lba = 18
    l_lba = next_lba; next_lba += sectors(table_size)
    m_lba = next_lba; next_lba += sectors(table_size)
    ce_lba = next_lba; next_lba += 1
    for d in directories: d.lba = next_lba; next_lba += d.size // SECTOR
    files = [c for d in directories for c in d.children if c.kind == 'file']
    for f in files:
        f.size = len(f.data); f.lba = next_lba if f.size else 0; next_lba += sectors(f.size)
    total = next_lba

    pvd = bytearray(SECTOR)
    pvd[0:7] = b'\1CD001\1'
    pvd[8:40] = text(b'', 32); pvd[40:72] = text(VOLUME, 32)
    pvd[80:88] = both32(total); pvd[120:124] = both16(1); pvd[124:128] = both16(1); pvd[128:132] = both16(SECTOR)
    pvd[132:140] = both32(table_size); pvd[140:144] = struct.pack('<I', l_lba); pvd[148:152] = struct.pack('>I', m_lba)
    pvd[156:190] = record(root.lba, root.size, True, b'\0', b'', stamp)
    for start, size in ((190, 128), (318, 128), (446, 128), (702, 37), (739, 37), (776, 37)): pvd[start:start + size] = text(b'', size)
    pvd[574:702] = text(VOLUME, 128)
    pvd[813:830] = voldate; pvd[830:847] = voldate; pvd[847:864] = b'0' * 16 + b'\0'; pvd[864:881] = b'0' * 16 + b'\0'
    pvd[881] = 1
    terminator = b'\xffCD001\1'.ljust(SECTOR, b'\0')

    with open(output, 'wb') as image:
        image.write(b'\0' * 16 * SECTOR + bytes(pvd) + terminator)
        for big in (False, True):
            data = table(big); image.write(data + b'\0' * (sectors(table_size) * SECTOR - len(data)))
        image.write(er.ljust(SECTOR, b'\0'))
        for d in directories: image.write(pack(list(entries(d))))
        for f in files: image.write(f.data + b'\0' * ((-len(f.data)) % SECTOR))
        if image.tell() != total * SECTOR: raise SystemExit('Image layout size mismatch')
    return {d.path: d.lba for d in directories} | {f.path: f.lba for f in files}



def read_back(path):
    """Parse the image as a Rock Ridge reader would and return {path: (mode, bytes-or-target)}."""
    data = Path(path).read_bytes()
    pvd = data[16 * SECTOR:17 * SECTOR]
    assert pvd[0:6] == b'\1CD001' and data[17 * SECTOR] == 255
    def entries(raw):
        fields, i = {}, 0
        while i + 4 <= len(raw) and raw[i:i + 2] != b'\0\0':
            sig, length = raw[i:i + 2], raw[i + 2]
            if length < 4: break
            fields.setdefault(sig, []).append(raw[i:i + length]); i += length
        return fields
    def parse(offset):
        length = data[offset]
        lba, size = struct.unpack('<I', data[offset + 2:offset + 6])[0], struct.unpack('<I', data[offset + 10:offset + 14])[0]
        name_len = data[offset + 32]; ident = data[offset + 33:offset + 33 + name_len]
        su_start = offset + 33 + name_len + (0 if name_len % 2 else 1)
        return lba, size, data[offset + 25] & 2, ident, entries(data[su_start:offset + length])
    root_lba, root_size, *_ = parse(16 * SECTOR + 156)
    first = parse(root_lba * SECTOR)[4]
    assert first[b'SP'][0][4:6] == b'\xbe\xef', 'missing SUSP indicator'
    ce = first[b'CE'][0]; ce_lba, ce_len = struct.unpack('<I', ce[4:8])[0], struct.unpack('<I', ce[20:24])[0]
    assert b'RRIP_1991A' in data[ce_lba * SECTOR:ce_lba * SECTOR + ce_len], 'missing Rock Ridge extension reference'
    found = {}
    def walk_dir(lba, size, prefix):
        offset, end = lba * SECTOR, lba * SECTOR + size
        while offset < end:
            if data[offset] == 0: offset = (offset // SECTOR + 1) * SECTOR; continue
            child_lba, child_size, is_dir, ident, fields = parse(offset); offset += data[offset]
            if ident in (b'\0', b'\1'): continue
            name = fields[b'NM'][0][5:].decode('utf-8'); mode = struct.unpack('<I', fields[b'PX'][0][4:8])[0]
            path = prefix + name
            if b'SL' in fields:
                raw, i, parts = fields[b'SL'][0][5:], 0, []
                while i < len(raw):
                    flags, size_ = raw[i], raw[i + 1]
                    parts.append('' if flags & 0x08 else raw[i + 2:i + 2 + size_].decode('utf-8')); i += 2 + size_
                found[path] = (mode, '/'.join(parts) if parts[0] else '/' + '/'.join(parts[1:]))
            elif is_dir:
                found[path] = (mode, None); walk_dir(child_lba, child_size, path + '/')
            else:
                found[path] = (mode, data[child_lba * SECTOR:child_lba * SECTOR + child_size])
    walk_dir(root_lba, root_size, '')
    return found


def main():
    archive, output, background, certificate = (Path(p).resolve() for p in sys.argv[1:5])
    image = archive_entries(archive)
    identifier = plistlib.loads(image['mac-lan.app/Contents/Info.plist'][1])['CFBundleIdentifier']
    image['Applications'] = (0o120755, '/Applications')
    image['.background'] = (0o40755, None)
    image['.background/background.png'] = (0o100644, background.read_bytes())
    window, icons = (200, 120, 640, 420), {'mac-lan.app': (170, 200), 'Applications': (470, 200)}
    now = time.time()
    # Two passes: the view settings have a fixed size, so the first layout gives the
    # extents Finder uses as file IDs for the background picture's alias.
    image['.DS_Store'] = (0o100644, macos_dsstore.write(window, icons))
    extents = build(tree(image), output, now)
    picture = macos_dsstore.alias(VOLUME.decode(), int(now), extents['.background'] * SECTOR,
                                  extents['.background/background.png'] * SECTOR, '.background', 'background.png')
    image['.DS_Store'] = (0o100644, macos_dsstore.write(window, icons, picture))
    if build(tree(image), output, now) != extents: raise SystemExit('Disk image layout changed between passes')
    expected = {k: (m, d) for k, (m, d) in image.items()}
    actual = read_back(output)
    if actual != expected:
        differing = sorted(set(expected) ^ set(actual)) or sorted(k for k in expected if expected[k] != actual.get(k))
        raise SystemExit('Disk image differs from the signed app: ' + ', '.join(differing[:5]))
    for name in ('mac-lan.app/Contents/MacOS/mac-lan', 'mac-lan.app/Contents/Helpers/MacDowHost'):
        assert actual[name][0] & 0o111, name + ' lost its executable mode'
    resources = actual['mac-lan.app/Contents/_CodeSignature/CodeResources'][1]
    cert = certificate.read_bytes()
    helper = macos_codesign.verify_macho(actual['mac-lan.app/Contents/Helpers/MacDowHost'][1], identifier + '.helper', {}, cert)
    assert plistlib.loads(resources)['files2']['Helpers/MacDowHost']['cdhash'] == helper
    macos_codesign.verify_macho(actual['mac-lan.app/Contents/MacOS/mac-lan'][1], identifier, {
        1: hashlib.sha256(actual['mac-lan.app/Contents/Info.plist'][1]).digest(), 3: hashlib.sha256(resources).digest()}, cert)
    view = {(n, c): p for n, c, _, p in macos_dsstore.read(actual['.DS_Store'][1])}
    assert plistlib.loads(view[('.', b'icvp')])['backgroundImageAlias'] == picture and (('mac-lan.app', b'Iloc') in view)
    print(f'Mac disk image verified: {len(expected)} entries, certificate-signed app bundle, Applications link and install window settings.')


if __name__ == '__main__':
    main()
