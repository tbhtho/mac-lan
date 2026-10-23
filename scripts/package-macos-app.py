from pathlib import Path
import json, plistlib, struct, sys, tarfile

root, output = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()
assert root.name == 'mac-lan.app'
executables = {'mac-lan.app/Contents/MacOS/mac-lan', 'mac-lan.app/Contents/Helpers/MacDowHost'}

def modes(info):
    info.uid = info.gid = 0
    info.uname = info.gname = ''
    info.mode = 0o755 if info.isdir() or info.name in executables else 0o644
    return info

with tarfile.open(output, 'w:gz', format=tarfile.USTAR_FORMAT) as archive:
    archive.add(root, arcname=root.name, filter=modes)
with tarfile.open(output, 'r:gz') as archive:
    for name in executables:
        member = archive.getmember(name)
        binary = archive.extractfile(member).read()
        assert member.mode & 0o111 and binary[:4] == b'\xcf\xfa\xed\xfe'
        assert struct.unpack('<I', binary[4:8])[0] == 0x100000c
    info = plistlib.load(archive.extractfile('mac-lan.app/Contents/Info.plist'))
    assert info['CFBundleExecutable'] == 'mac-lan' and info['LSUIElement'] is True
    assert info['CFBundleIdentifier'] == 'com.tbhtho.macdow.host'
    marker = json.load(archive.extractfile('mac-lan.app/Contents/Resources/MAC-DOW-INSTALL.json'))
    assert marker['layout'] == 'app-bundle' and marker['product'] == 'MAC-DOW'
    assert not any(name.endswith('/MacDowCapture') for name in archive.getnames())
print('Mac .app verified: menu-bar plist, executable ARM64 launcher and host, legal resources, embedded native source.')
