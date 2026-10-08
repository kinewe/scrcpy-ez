"""Verify a clean Windows release, its fresh profile and both hash manifests."""
from pathlib import Path
import hashlib
import json
import re
import sys
import zipfile

REQUIRED = {
    'scrcpy-ez.exe', 'scrcpy.exe', 'scrcpy-server', 'scrcpy-server-legacy',
    'adb.exe', 'AdbWinApi.dll', 'AdbWinUsbApi.dll', 'SDL3.dll',
    'WebView2Loader.dll', '投屏支持.bat', 'profiles.json', 'README.txt',
    'LICENSE.txt', 'Lucide图标许可.txt', 'FILE_SHA256SUMS.txt',
}
FORBIDDEN = {'config.txt', 'config.identity.txt', 'settings.json', 'root-repair.json', 'scrcpy_frame_log.txt'}


def verify(path):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        assert len(set(names)) == len(names), 'Duplicate archive entries'
        assert REQUIRED <= set(names), 'Missing runtime: ' + repr(REQUIRED - set(names))
        assert not (FORBIDDEN & set(names)), 'Private configuration in archive'
        assert all('/' not in name and '\\' not in name for name in names), 'Unexpected nested path'
        assert all(not re.search(r'\.bak|\.(old|log|lnk|url|ico|vbs|ps1|py|pem|key|env|pdb|jpg|jpeg)$', name, re.I)
                   and not name.startswith('TSF') for name in names), 'Diagnostic or private files'
        assert all(not name.endswith('.png') or name in ('scrcpy.png', 'disconnected.png') for name in names), 'Unexpected icon or screenshot'
        assert archive.testzip() is None, 'ZIP CRC failure'
        profile = json.loads(archive.read('profiles.json'))
        assert profile == {'devices': {}, 'deviceOrder': []}, 'Device profile is not fresh'
        rows = [line.split('  ', 1) for line in archive.read('FILE_SHA256SUMS.txt').decode('utf-8').splitlines()]
        assert {name for _, name in rows} == set(names) - {'FILE_SHA256SUMS.txt'}, 'File hash coverage mismatch'
        assert len(rows) == len(names) - 1, 'Duplicate file hashes'
        markers = [b'-----BEGIN PRIVATE KEY-----', b'-----BEGIN OPENSSH PRIVATE KEY-----', b'GITHUB_TOKEN=', b'ghp_', b'github_pat_']
        for digest, name in rows:
            data = archive.read(name)
            assert hashlib.sha256(data).hexdigest().upper() == digest.upper(), 'File hash mismatch: ' + name
            assert all(marker not in data for marker in markers), 'Credential marker: ' + name
        for name in ('scrcpy-ez.exe', 'scrcpy.exe'):
            data = archive.read(name)
            assert all(marker not in data for marker in (b'C:\\Dev\\', b'C:/Dev/', b'C:\\Users\\', b'C:/Users/')), 'Local build path in ' + name
        gui = archive.read('scrcpy-ez.exe')
        pe = int.from_bytes(gui[0x3c:0x40], 'little')
        assert int.from_bytes(gui[pe+0x5c:pe+0x5e], 'little') == 2, 'GUI must use Windows subsystem'
    sums = path.with_name('SHA256SUMS.txt').read_text(encoding='utf-8').splitlines()
    expected = [line.split('  ', 1)[0] for line in sums if line.split('  ', 1)[1] == path.name]
    assert len(expected) == 1 and hashlib.sha256(path.read_bytes()).hexdigest().upper() == expected[0].upper(), 'Archive hash mismatch'
    print(f'OK: {path.name}; {len(names)} entries, fresh device profile, runtime, CRC, privacy and SHA256 verified')


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit('Usage: verify_package.py RELEASE.zip')
    verify(Path(sys.argv[1]))
