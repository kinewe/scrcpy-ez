"""Build the Windows power shortcut test with an existing Ninja release build."""
from pathlib import Path
import argparse
import json
import shlex
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--build-dir', type=Path, required=True)
parser.add_argument('--compiler', required=True)
parser.add_argument('--ninja', default='ninja')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[2]
build = args.build_dir.resolve()
obj = build / 'power-shortcut-test.obj'
exe = build / 'dist/power-shortcut-test.exe'
exe.parent.mkdir(parents=True, exist_ok=True)


def command(target):
    output = subprocess.check_output([args.ninja, '-C', str(build), '-t', 'commands', target], text=True)
    invocation = shlex.split(output.splitlines()[-1])
    if any(arg.startswith('@') for arg in invocation[1:]):
        # Newer Ninja builds use response files, which are removed after linking.
        # compdb -x expands their contents directly from the build description.
        rows = json.loads(subprocess.check_output(
            [args.ninja, '-C', str(build), '-t', 'compdb', '-x'], text=True))
        invocation = shlex.split(next(row['command'] for row in rows if row['output'] == target))
    invocation[0] = args.compiler
    return invocation


compile_args = command('app/scrcpy.exe.p/src_input_manager.c.obj')
compile_args = compile_args[:compile_args.index('-MD')]
compile_args += ['-c', str(repo / 'app/tests/test_power_shortcut.c'), '-o', str(obj)]
subprocess.run(compile_args, cwd=build, check=True)
link_args = command('app/scrcpy.exe')
link_args[link_args.index('-o') + 1] = str(exe)
link_args = [str(obj) if a.endswith('src_main.c.obj') else a for a in link_args]
link_args = [a for a in link_args if not a.endswith('src_input_manager.c.obj')]
subprocess.run(link_args, cwd=build, check=True)
result = subprocess.run([str(exe)], cwd=exe.parent, capture_output=True, text=True, encoding='utf-8', timeout=15)
print(result.stdout)
if result.returncode:
    print(result.stderr)
    raise SystemExit(result.returncode)
