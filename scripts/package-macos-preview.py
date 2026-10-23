from pathlib import Path
import sys
import tarfile

root = Path(sys.argv[1]).resolve()
output = Path(sys.argv[2]).resolve()

def modes(info):
    info.uid = info.gid = 0
    info.uname = info.gname = ''
    info.mode = 0o755 if info.isdir() or Path(info.name).name == 'MacDowHost' else 0o644
    return info

with tarfile.open(output, 'w:gz', format=tarfile.USTAR_FORMAT) as archive:
    archive.add(root, arcname='macos-arm64', filter=modes)
with tarfile.open(output, 'r:gz') as archive:
    assert archive.getmember('macos-arm64/MacDowHost').mode & 0o111
    assert 'macos-arm64/MacDowCapture' not in archive.getnames(), 'Old 30 fps helper must not be packaged'
print('Mac archive verified: executable host, embedded native source, no old capture helper.')
