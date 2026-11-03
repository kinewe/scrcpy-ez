"""Host-owned bounded experiment and cleanup, including abnormal app_process exits."""
from pathlib import Path
import argparse
import json
import os
import re
import subprocess
import time

folder = Path(__file__).resolve().parent
repo = folder.parents[1]
dev = Path(os.environ.get('SCEZ_DEV_ROOT', repo.parent))
parser = argparse.ArgumentParser()
parser.add_argument('--serial', required=True)
parser.add_argument('--label', required=True)
parser.add_argument('--mode', choices=['texture', 'adapt', 'generic'], default='texture')
args = parser.parse_args()
assert re.fullmatch(r'[A-Za-z0-9_-]+', args.label)
adb = str(dev / 'tools/Android/platform-tools/adb.exe')
out = folder / 'out'
prefix = out / args.label
assert not prefix.with_suffix('.scene.log').exists(), 'Use a new label'
remote_probe = '/data/local/tmp/yinmo-texture-20261102-probes.zip'
remote_base = '/data/local/tmp/yinmo-texture-20261102-base.jar'
classpath = f'CLASSPATH={remote_probe}:{remote_base}'
pkg = 'com.eg.android.AlipayGphone'


def command(*values, timeout=15, check=True):
    p = subprocess.run([adb, '-s', args.serial, *map(str, values)], capture_output=True, timeout=timeout)
    text = (p.stdout + p.stderr).decode('utf8', errors='replace')
    if check and p.returncode:
        raise RuntimeError(text[-2500:])
    return text


process = None
phone_pid = None
association = None
display = None
cleanup = {}
try:
    command('push', folder / 'out/probes.zip', remote_probe)
    command('push', repo / 'server/build/outputs/apk/release/server-release-unsigned.apk', remote_base)
    with prefix.with_suffix('.scene.log').open('wb') as log:
        process = subprocess.Popen([adb, '-s', args.serial, 'shell', f'{classpath} app_process / com.genymobile.scrcpy.VirtualInputScene {pkg}'], stdout=log, stderr=subprocess.STDOUT, creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            text = prefix.with_suffix('.scene.log').read_text(encoding='utf8', errors='replace')
            found = re.search(r'OWNED_ASSOCIATION (02:53:[0-9a-f:]+)', text)
            if found: association = found[1]
            ready = re.search(r'VIRTUAL_SCENE_READY display=(\d+) pid=(\d+)', text)
            if ready:
                display, phone_pid = map(int, ready.groups())
                break
            if process.poll() is not None: raise RuntimeError(text[-3000:])
            time.sleep(.2)
        if display is None: raise TimeoutError('scene readiness')
        print(f'READY display={display} phonePid={phone_pid}', flush=True)
        time.sleep(7)
        camera = command('shell', 'dumpsys media.camera', timeout=20)
        prefix.with_suffix('.camera.log').write_text('\n'.join(camera.splitlines()[:160]), encoding='utf8')
        name = {'texture': 'TexturePathProbe', 'adapt': 'WindowAdaptationProbe', 'generic': 'GenericUiProbe'}[args.mode]
        tail = str(display) if args.mode in ('adapt', 'generic') else '30'
        result = command('shell', f'{classpath} app_process / com.genymobile.scrcpy.{name} {pkg} {tail}', timeout=40, check=False)
        prefix.with_suffix('.probe.log').write_text(result, encoding='utf8')
        print(result[-11000:], flush=True)
        if 'RESOURCES_RELEASED' not in result or ('Exception' in result and '_JSON ' not in result):
            raise RuntimeError('probe did not complete successfully; inspect saved log')
finally:
    if phone_pid is not None:
        if display is not None:
            ended = command('shell', f'{classpath} app_process / com.genymobile.scrcpy.SceneCleanupProbe {pkg} {display}', timeout=20, check=False)
            cleanup['scopedScannerCleanupConfirmed'] = 'SCOPED_CHILD_FINISHED' in ended
            prefix.with_suffix('.scanner-cleanup.log').write_text(ended, encoding='utf8')
        command('shell', 'touch', f'/data/local/tmp/yinmo-texture-stop-{phone_pid}', check=False)
    if process is not None:
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            if phone_pid is not None:
                identity = command('shell', 'cat', f'/proc/{phone_pid}/cmdline', check=False)
                if 'com.genymobile.scrcpy.VirtualInputScene' in identity:
                    command('shell', 'kill', '-TERM', phone_pid, check=False)
                    cleanup['terminatedOwnedScene'] = True
            try: process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=5)
    if association:
        command('shell', 'cmd', 'companiondevice', 'disassociate', '0', 'com.android.shell', association, check=False)
        cleanup['ownedAssociationRemaining'] = association in command('shell', 'cmd', 'companiondevice', 'list', '0')
    command('shell', 'rm', '-f', remote_probe, remote_base, *([f'/data/local/tmp/yinmo-texture-stop-{phone_pid}'] if phone_pid is not None else []), check=False)
    displays = command('shell', 'dumpsys display')
    cleanup['ownedDisplayRemaining'] = display is not None and bool(re.search(r'mDisplayId\s*=\s*'+str(display)+r'\b', displays))
    cleanup['sceneExitCode'] = None if process is None else process.poll()
    cleanup['adbForwards'] = command('forward', '--list').strip()
    cleanup['cameraAfter'] = '\n'.join(command('shell', 'dumpsys media.camera').splitlines()[:15])
    prefix.with_suffix('.cleanup.json').write_text(json.dumps(cleanup, ensure_ascii=False, indent=2), encoding='utf8')
    print('CLEANUP', json.dumps(cleanup, ensure_ascii=False), flush=True)
    if cleanup.get('ownedAssociationRemaining') or cleanup['ownedDisplayRemaining']:
        raise RuntimeError('owned resources remain; inspect cleanup report')
