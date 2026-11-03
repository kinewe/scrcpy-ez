"""Store only the current camera-owner section; omit historical camera events."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json, subprocess, sys, time

label, serial, package = sys.argv[1:4]
assert label.replace('-', '').isalnum()
adb = research_config.adb()
dump = subprocess.check_output([adb, '-P', research_config.adb_port(), '-s', serial, 'shell', 'dumpsys', 'media.camera'], timeout=20).decode('utf8', errors='replace')
prefix = dump.split('== Camera service events log', 1)[0]
active = prefix.split('Active Camera Clients:', 1)[1] if 'Active Camera Clients:' in prefix else ''
lines = [line.strip() for line in active.splitlines() if 'Client Package Name: ' + package + ',' in line]
report = dict(package=package, epoch=time.time(), activeOwnerMatches=bool(lines), evidence=lines)
Path(__file__).with_name('out').joinpath(label + '-camera-client.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf8')
print(json.dumps(report, ensure_ascii=False))
