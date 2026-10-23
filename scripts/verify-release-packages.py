"""Check actual public package identities before immutable release publication."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import tarfile
import plistlib
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import macos_codesign  # noqa: E402  (sibling build module)

parser = argparse.ArgumentParser()
parser.add_argument('--windows', required=True, type=Path)
parser.add_argument('--mac', required=True, type=Path)
parser.add_argument('--mac-app', type=Path)
parser.add_argument('--commit', required=True)
parser.add_argument('--version', required=True)
args = parser.parse_args()
webhook = re.compile(rb'https://(?:discord(?:app)?\.com)/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]{50,}')

def public(data):
    if webhook.search(data):
        raise ValueError('A public package contains a concrete diagnostic credential')

def identity(marker, platform, layout):
    expected = dict(product='MAC-DOW', repositoryId=1430459188,
                    version=args.version, commit=args.commit, os=platform,
                    arch='amd64' if platform == 'windows' else 'arm64', layout=layout)
    if any(marker.get(k) != v for k, v in expected.items()):
        raise ValueError('Package identity differs from the intended release')

exe = args.windows.read_bytes()
public(exe)
if exe[:2] != b'MZ' or exe[-8:] != b'MCDOWPK1':
    raise ValueError('Windows package is missing its executable/identity footer')
size = struct.unpack('<I', exe[-12:-8])[0]
if not 0 < size <= 16384 or size > len(exe) - 12:
    raise ValueError('Windows package identity footer has an invalid size')
marker = json.loads(exe[-12-size:-12])
identity(marker, 'windows', 'single-exe')
if marker.get('dirty') is not False:
    raise ValueError('Windows release was built from dirty source')
with tarfile.open(args.mac, 'r:gz') as archive:
    for member in archive.getmembers():
        if member.isfile():
            public(archive.extractfile(member).read())
    host = archive.getmember('macos-arm64/MacDowHost')
    binary = archive.extractfile(host).read()
    if not host.mode & 0o111 or binary[:4] != b'\xcf\xfa\xed\xfe' or struct.unpack('<I', binary[4:8])[0] != 0x100000c:
        raise ValueError('Mac host must be an executable ARM64 Mach-O')
    identity(json.load(archive.extractfile('macos-arm64/MAC-DOW-INSTALL.json')), 'darwin', 'commandline-preview')
    provenance = json.load(archive.extractfile('macos-arm64/BUILD-PROVENANCE.json'))
    source_hash = hashlib.sha256(Path('native/MacDowCapture.swift').read_bytes()).hexdigest()
    if (provenance.get('hostCommit') != args.commit or provenance.get('dirty') is not False
            or provenance.get('publicUpdate') is not True
            or provenance.get('captureSourceSha256') != source_hash or source_hash.encode() not in binary):
        raise ValueError('Mac provenance must match clean public source')
    if archive.extractfile('macos-arm64/COMMIT.txt').read().decode().strip() != args.commit:
        raise ValueError('Mac source commit differs from the release')
    menu_source = Path('native/HostUpdater.swift').read_bytes() + b'\n' + Path('native/MacDowHostApp.swift').read_bytes()
    menu_hash = hashlib.sha256(menu_source).hexdigest()
    if provenance.get('menuSourceSha256') != menu_hash or menu_hash.encode() not in binary:
        raise ValueError('Mac preview does not contain the current verified menu source')
if args.mac_app:
    with tarfile.open(args.mac_app, 'r:gz') as archive:
        for member in archive.getmembers():
            if member.isfile():public(archive.extractfile(member).read())
        prefix='mac-lan.app/Contents/'
        # The app's executables are the compatibility host re-signed with the mac-lan certificate.
        certificate=Path('installer/macos-codesign.cer').read_bytes()
        info_plist=archive.extractfile(prefix+'Info.plist').read()
        resources=archive.extractfile(prefix+'_CodeSignature/CodeResources').read()
        for name,identifier,slots in [('MacOS/mac-lan','com.tbhtho.macdow.host',{1:hashlib.sha256(info_plist).digest(),3:hashlib.sha256(resources).digest()}),('Helpers/MacDowHost','com.tbhtho.macdow.host.helper',{})]:
            member=archive.getmember(prefix+name)
            content=archive.extractfile(member).read()
            if not member.mode & 0o111 or macos_codesign.code_bytes(content) != macos_codesign.code_bytes(binary):
                raise ValueError('Mac app executable differs from the verified compatibility host')
            macos_codesign.verify_macho(content,identifier,slots,certificate)
        identity(json.load(archive.extractfile(prefix+'Resources/MAC-DOW-INSTALL.json')),'darwin','app-bundle')
        app_provenance=json.load(archive.extractfile(prefix+'Resources/BUILD-PROVENANCE.json'))
        if app_provenance.get('hostCommit')!=args.commit or app_provenance.get('dirty') is not False or app_provenance.get('publicUpdate') is not True or app_provenance.get('menuSourceSha256')!=menu_hash or app_provenance.get('captureSourceSha256')!=source_hash:
            raise ValueError('Mac app provenance differs from the intended public source')
        info=plistlib.load(archive.extractfile(prefix+'Info.plist'))
        if info.get('CFBundleIdentifier')!='com.tbhtho.macdow.host' or info.get('CFBundleExecutable')!='mac-lan' or info.get('LSUIElement') is not True:
            raise ValueError('Mac app is missing its menu-bar application identity')
print('Public Windows and Mac package identities, provenance, and credential checks passed.')
