"""Finder view settings (.DS_Store) for the mac-lan disk image window.

Writes the buddy-allocated B-tree Finder reads: a fixed window without toolbar or
sidebar, large icons at fixed positions, and a background picture referenced by
an alias record that resolves by its path on the mounted volume.
"""
import plistlib, struct, time

HFS_EPOCH = 2082844800  # seconds from 1904-01-01 to 1970-01-01


def _ustr(text):
    raw = text.encode('utf-16-be')
    return struct.pack('>I', len(raw) // 2) + raw


def _record(name, code, kind, payload):
    return _ustr(name) + code + kind + payload


def alias(volume, volume_created, parent_cnid, target_cnid, directory, filename):
    """Alias version 2 record for /<directory>/<filename> on a volume named `volume`."""
    def pascal(value, size): return bytes([len(value)]) + value.ljust(size - 1, b'\0')
    created = int(volume_created) + HFS_EPOCH
    body = struct.pack('>h28sI2shI64sII4s4shhI2s10s', 0, pascal(volume.encode(), 28), created, b'H+', 5, parent_cnid,
                       pascal(filename.encode(), 64), target_cnid, created, b'\0' * 4, b'\0' * 4, -1, -1, 0, b'\0\0', b'\0' * 10)
    tags = [(0, directory.encode()), (1, struct.pack('>I', parent_cnid)),
            (2, f'{volume}:{directory}:{filename}'.encode()),
            (14, struct.pack('>H', len(filename)) + filename.encode('utf-16-be')),
            (15, struct.pack('>H', len(volume)) + volume.encode('utf-16-be')),
            (18, f'/{directory}/{filename}'.encode()), (19, f'/Volumes/{volume}'.encode())]
    extra = b''
    for tag, data in tags:
        extra += struct.pack('>hH', tag, len(data)) + data + (b'\0' if len(data) % 2 else b'')
    extra += struct.pack('>hH', -1, 0)
    record = b'\0' * 4 + struct.pack('>HH', 0, 2) + body + extra
    return record[:4] + struct.pack('>H', len(record)) + record[6:]


def write(window, icons, background_alias=None):
    """window: (x, y, width, height); icons: {name: (x, y)} centre positions."""
    x, y, w, h = window
    browser = {'ContainerShowSidebar': False, 'PreviewPaneVisibility': False, 'ShowPathbar': False, 'ShowSidebar': False,
               'ShowStatusBar': False, 'ShowTabView': False, 'ShowToolbar': False, 'SidebarWidth': 0,
               'WindowBounds': '{{%d, %d}, {%d, %d}}' % (x, y, w, h)}
    view = {'arrangeBy': 'none', 'backgroundColorBlue': 1.0, 'backgroundColorGreen': 1.0, 'backgroundColorRed': 1.0,
            'backgroundType': 1, 'gridOffsetX': 0.0, 'gridOffsetY': 0.0, 'gridSpacing': 100.0, 'iconSize': 128.0,
            'labelOnBottom': True, 'showIconPreview': True, 'showItemInfo': False, 'textSize': 13.0, 'viewOptionsVersion': 1}
    if background_alias:
        view['backgroundType'] = 2; view['backgroundImageAlias'] = background_alias
    def blob(value): return b'blob' + struct.pack('>I', len(value)) + value
    records = [('.', b'bwsp', blob(plistlib.dumps(browser, fmt=plistlib.FMT_BINARY))),
               ('.', b'icvp', blob(plistlib.dumps(view, fmt=plistlib.FMT_BINARY))),
               ('.', b'vSrn', b'long' + struct.pack('>I', 1))]
    for name, (ix, iy) in icons.items():
        records.append((name, b'Iloc', blob(struct.pack('>IIII', ix, iy, 0xFFFFFFFF, 0xFFFF0000))))
    records.sort(key=lambda r: (r[0].lower(), r[1]))
    leaf = struct.pack('>II', 0, len(records)) + b''.join(_record(n, c, d[:4], d[4:]) for n, c, d in records)
    if len(leaf) > 4096: raise ValueError('Finder view settings exceed one node')
    # Allocator space: header 0/32, DSDB 32/32, bookkeeping 2048/2048, leaf node 4096/4096.
    blocks = [(2048, 11), (32, 5), (4096, 12)]
    dsdb = struct.pack('>IIIII', 2, 0, len(records), 1, 0x1000)
    free = {i: [] for i in range(32)}
    for size_log in range(6, 11): free[size_log].append(1 << size_log)
    for size_log in range(13, 31): free[size_log].append(1 << size_log)
    book = struct.pack('>II', len(blocks), 0) + b''.join(struct.pack('>I', off | lg) for off, lg in blocks)
    book += b'\0' * 4 * (256 - len(blocks))
    book += struct.pack('>I', 1) + bytes([4]) + b'DSDB' + struct.pack('>I', 1)
    for i in range(32): book += struct.pack('>I', len(free[i])) + b''.join(struct.pack('>I', o) for o in free[i])
    space = bytearray(8192)
    space[0:32] = b'Bud1' + struct.pack('>III', 2048, len(book), 2048) + b'\0' * 16
    space[32:32 + len(dsdb)] = dsdb
    space[2048:2048 + len(book)] = book
    space[4096:4096 + len(leaf)] = leaf
    return struct.pack('>I', 1) + bytes(space)


def read(data):
    """Minimal reader used to check the writer: returns [(name, code, type, payload)]."""
    assert data[:4] == b'\0\0\0\1' and data[4:8] == b'Bud1'
    space = data[4:]
    book_off, = struct.unpack('>I', space[4:8])
    count, = struct.unpack('>I', space[book_off:book_off + 4])
    addresses = [struct.unpack('>I', space[book_off + 8 + 4 * i:book_off + 12 + 4 * i])[0] for i in range(count)]
    dir_at = book_off + 8 + 4 * ((count + 255) // 256 * 256)
    assert struct.unpack('>I', space[dir_at:dir_at + 4])[0] == 1 and space[dir_at + 5:dir_at + 9] == b'DSDB'
    dsdb_addr = addresses[struct.unpack('>I', space[dir_at + 9:dir_at + 13])[0]] & ~0x1F
    root, levels, nrecords = struct.unpack('>III', space[dsdb_addr:dsdb_addr + 12])
    node = addresses[root] & ~0x1F
    p, n = struct.unpack('>II', space[node:node + 8])
    assert p == 0 and levels == 0 and n == nrecords
    out, i = [], node + 8
    for _ in range(n):
        length, = struct.unpack('>I', space[i:i + 4]); name = space[i + 4:i + 4 + 2 * length].decode('utf-16-be'); i += 4 + 2 * length
        code, kind = space[i:i + 4], space[i + 4:i + 8]; i += 8
        if kind == b'blob':
            size, = struct.unpack('>I', space[i:i + 4]); payload = space[i + 4:i + 4 + size]; i += 4 + size
        elif kind == b'long':
            payload = space[i:i + 4]; i += 4
        else:
            raise ValueError(kind)
        out.append((name, code, kind, payload))
    return out
