#!/usr/bin/env python3
"""Build synthetic, opt-in device test fixtures; never installs an APK."""
import argparse, json, os, subprocess, uuid, zipfile
from pathlib import Path

p = argparse.ArgumentParser()
for arg in ['sdk', 'jdk', 'server-classes', 'out']:
    p.add_argument('--' + arg, required=True)
a = p.parse_args()
repo = Path(__file__).resolve().parent.parent
sdk, jdk, server = Path(a.sdk), Path(a.jdk), Path(a.server_classes).resolve()
android, tools = sdk / 'platforms/android-36/android.jar', sdk / 'build-tools/36.0.0'
out = Path(a.out).resolve() / ('fixtures-' + uuid.uuid4().hex)
out.mkdir(parents=True)
env = dict(os.environ, JAVA_HOME=str(jdk))
exe, bat = ('.exe', '.bat') if os.name == 'nt' else ('', '')

def run(args):
    subprocess.run([str(x) for x in args], env=env, check=True,
                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)

def dex(source, name, cp=None):
    root = out / name
    classes, target = root / 'classes', root / 'dex'
    classes.mkdir(parents=True); target.mkdir()
    run([jdk / ('bin/javac' + exe), '-encoding', 'UTF-8', '-source', '8', '-target', '8',
         '-cp', str(android) + (os.pathsep + str(cp) if cp else ''), '-d', classes, *sorted(source.glob('*.java'))])
    jar = root / 'classes.jar'
    with zipfile.ZipFile(jar, 'w') as z:
        for f in classes.rglob('*.class'): z.write(f, f.relative_to(classes).as_posix())
    command = [tools / ('d8' + bat), '--min-api', '26', '--lib', android]
    run(command + (['--classpath', cp] if cp else []) + ['--output', target, jar])
    return root, target / 'classes.dex'

_, shell_dex = dex(repo / 'gui/internal/notifications/testdata/notification-detail', 'shell', server)
shell = out / 'notification-detail-fixture.jar'
with zipfile.ZipFile(shell, 'w') as z: z.write(shell_dex, 'classes.dex')
app_source = repo / 'gui/internal/app/testdata/notification-app'
app_root, app_dex = dex(app_source, 'app')
unsigned, aligned, key = app_root / 'unsigned.apk', app_root / 'aligned.apk', app_root / 'fixture.jks'
run([tools / ('aapt2' + exe), 'link', '-I', android, '--manifest', app_source / 'AndroidManifest.xml', '-o', unsigned])
with zipfile.ZipFile(unsigned, 'a') as z: z.write(app_dex, 'classes.dex')
run([tools / ('zipalign' + exe), '-f', '4', unsigned, aligned])
run([jdk / ('bin/keytool' + exe), '-genkeypair', '-keystore', key, '-storepass', 'lab-only', '-keypass', 'lab-only',
     '-alias', 'fixture', '-dname', 'CN=ez notification test', '-keyalg', 'RSA', '-validity', '2'])
apk = out / 'notification-app-fixture.apk'
run([tools / ('apksigner' + bat), 'sign', '--ks', key, '--ks-pass', 'pass:lab-only', '--out', apk, aligned])
print(json.dumps({'shellFixture': str(shell), 'appFixture': str(apk)}))
