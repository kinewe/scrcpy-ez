"""Read package declarations on explicit devices; remove only the current probe JAR."""
from pathlib import Path
import argparse
import json
import os
import subprocess
import sys
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import research_common as config

PACKAGES = ['com.eg.android.AlipayGphone', 'com.tencent.wework', 'com.xingin.xhs', 'com.oray.sunlogin', 'com.tencent.mm']


def inventory(adb, port, devices, jar, runner=subprocess.run):
    results = []
    for serial in devices:
        config.validate_serial(serial)
        remote = '/data/local/tmp/rc25_graphics_capability_' + uuid.uuid4().hex + '.jar'

        def run(*args):
            return runner([adb, '-P', port, '-s', serial, *args], capture_output=True,
                          text=True, encoding='utf8', errors='replace', timeout=30)

        entry = {'serial': serial, 'ok': False}
        try:
            pushed = run('push', str(jar), remote)
            if pushed.returncode:
                raise RuntimeError(pushed.stderr)
            result = run('shell', 'env', 'CLASSPATH=' + remote, 'app_process', '/',
                         'com.genymobile.scrcpy.GraphicsCaptureCapabilityProbe', *PACKAGES)
            line = next((line for line in result.stdout.splitlines()
                         if line.startswith('GRAPHICS_CAPABILITIES_JSON ')), None)
            if result.returncode or not line:
                raise RuntimeError(result.stdout + result.stderr)
            entry.update(json.loads(line.split(' ', 1)[1]))
            entry['ok'] = True
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
            entry['error'] = str(error)
        finally:
            try:
                removed = run('shell', 'rm', '-f', remote)
                exists = run('shell', 'if [ -e ' + remote + ' ]; then echo PRESENT; else echo ABSENT; fi')
                absent = removed.returncode == 0 and exists.returncode == 0 and exists.stdout.strip() == 'ABSENT'
                entry['cleanup'] = {'cleanupExit': removed.returncode, 'temporaryJarAbsent': absent}
                entry['ok'] = entry['ok'] and absent
            except (OSError, subprocess.SubprocessError) as error:
                entry['cleanup'] = {'temporaryJarAbsent': False, 'error': str(error)}
                entry['ok'] = False
        results.append(entry)
    return results


def main():
    folder = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', action='append', help='Explicit device; repeat for multiple devices')
    parser.add_argument('--adb', default=os.environ.get('ADB', 'adb'))
    parser.add_argument('--adb-port', default=config.adb_port())
    parser.add_argument('--jar', type=Path, default=folder / 'out/probes.zip')
    parser.add_argument('--output', type=Path, default=folder / 'out/graphics-capabilities.json')
    args = parser.parse_args()
    if not args.adb_port.isdecimal() or not 1 <= int(args.adb_port) <= 65535:
        parser.error('--adb-port must be between 1 and 65535')
    if not args.jar.is_file():
        parser.error('Build the probe JAR first, or provide --jar')
    devices = args.serial or config.serials()
    if len(devices) != len(set(devices)):
        parser.error('Each device must appear only once')
    results = inventory(args.adb, args.adb_port, devices, args.jar)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding='utf8')
    for entry in results:
        print(json.dumps(entry, ensure_ascii=False))
    return 0 if all(entry['ok'] for entry in results) else 1


if __name__ == '__main__':
    raise SystemExit(main())
