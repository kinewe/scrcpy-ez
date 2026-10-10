"""Build the current Android helper with an already-installed SDK/JDK, without Gradle/network."""
from pathlib import Path
import argparse, re, subprocess, zipfile

parser = argparse.ArgumentParser()
parser.add_argument('--sdk', required=True)
parser.add_argument('--jdk', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
out = Path(args.out).resolve()
out.mkdir(parents=True, exist_ok=True)
# New build directory each time: retain diagnostics and never delete computed paths.
import uuid
build = out / ('server-' + uuid.uuid4().hex)
build.mkdir()
classes, gen = build/'classes', build/'gen'
classes.mkdir(); (gen/'com/genymobile/scrcpy').mkdir(parents=True)
version = re.search(r"version:\s*'([^']+)'", (root/'meson.build').read_text(encoding='utf-8')).group(1)
(gen/'com/genymobile/scrcpy/BuildConfig.java').write_text(
    'package com.genymobile.scrcpy; public final class BuildConfig { public static final boolean DEBUG=false; '
    f'public static final String VERSION_NAME="{version}"; }}', encoding='utf-8')
sdk, jdk = Path(args.sdk), Path(args.jdk)
tools = sdk/'build-tools/36.0.0'
android = sdk/'platforms/android-36/android.jar'
aidl = root/'server/src/main/aidl'
def run(command, cwd=None):
 subprocess.run([str(arg) for arg in command],cwd=cwd,check=True)
run([tools/'aidl.exe', '-o'+str(gen), '-I'+str(aidl), aidl/'android/content/IOnPrimaryClipChangedListener.aidl'],aidl)
run([tools/'aidl.exe', '-o'+str(gen), '-I'+str(aidl), '-p',android.parent/'framework.aidl',aidl/'android/view/IDisplayWindowListener.aidl'],aidl)
sources = list((root/'server/src/main/java').rglob('*.java')) + list(gen.rglob('*.java'))
source_list = build/'sources.txt'
source_list.write_text('\n'.join('"'+str(path).replace('\\','/')+'"' for path in sources),encoding='utf-8')
run([jdk/'bin/javac.exe','-encoding','UTF-8','-bootclasspath',android,'-cp',tools/'core-lambda-stubs.jar','-source','8','-target','8','-d',classes,'@'+str(source_list)])
compiled = build/'classes.jar'
with zipfile.ZipFile(compiled,'w') as archive:
 for path in classes.rglob('*.class'):
  archive.write(path,path.relative_to(classes).as_posix())
binary=build/'scrcpy-server'
run([jdk/'bin/java.exe','-cp',tools/'lib/d8.jar','com.android.tools.r8.D8','--min-api','21','--classpath',android,'--output',build/'dex.zip',compiled])
(build/'dex.zip').rename(binary)
print('SERVER_OUTPUT='+str(binary))
