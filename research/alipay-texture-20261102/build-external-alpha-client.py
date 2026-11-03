"""Create a research-only client with automatic alpha adaptation disabled.

Patch a generated copy of scrcpy.c, leaving product sources and the regular
client unchanged. Desktop input, virtual camera and ordinary video stay active.
"""
from pathlib import Path
import hashlib
import json
import os
import re
import subprocess

repo = Path(__file__).resolve().parents[2]
dev = Path(os.environ.get('SCEZ_DEV_ROOT', repo.parent))
build = dev / 'build/rc25-research-20261102'
out = build / 'external-alpha'
out.mkdir(exist_ok=True)
source = (repo / 'app/src/scrcpy.c').read_text(encoding='utf8')
needle = 'if (!sc_native_source_start(native_source_serial, target_package)) {'
assert source.count(needle) == 1
patched = source.replace(needle, 'LOGI("RESEARCH: automatic native-alpha adaptation disabled; external probe owns adaptation");\n        if (false) {')
copy = out / 'scrcpy-external-alpha.c'
copy.write_text(patched, encoding='utf8')
split = lambda s: [a or b for a, b in re.findall(r'"([^"]*)"|(\S+)', s)]
entry = next(e for e in json.loads((build / 'compile_commands.json').read_text()) if e['file'].replace('\\','/').endswith('/scrcpy.c'))
command = split(entry['command'])
command[0] = str(dev / 'tools/msys64/mingw64/bin/gcc.exe')
command[-1] = str(copy)
obj = out / 'scrcpy-external-alpha.obj'
for opt in ['-o', '-MF', '-MQ']:
    command[command.index(opt)+1] = str(obj) + ('.d' if opt == '-MF' else '')
subprocess.run(command, cwd=build, check=True)
lines = (build / 'build.ninja').read_text().splitlines()
index = next(i for i, s in enumerate(lines) if s.startswith('build app/scrcpy.exe:'))
objects = lines[index].split('c_LINKER_RSP ',1)[1].split(' | ',1)[0].split()
objects = [str(obj) if s == entry['output'].replace('\\','/') else s for s in objects]
assert str(obj) in objects
link = split(lines[index+1].split(' = ',1)[1])
target = build / 'dist-encoders/scrcpy-external-alpha.exe'
subprocess.run([command[0], '-o', str(target), *objects, *link], cwd=build, check=True)
print(target)
print('SHA256', hashlib.sha256(target.read_bytes()).hexdigest())
