"""Build an independent probe using the local SDK and compiled product classes."""
from pathlib import Path
import os
import subprocess
import argparse

folder = Path(__file__).resolve().parent
repo = folder.parents[1]
dev = Path(os.environ.get('SCEZ_DEV_ROOT', repo.parent))
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--sdk-root', type=Path, default=Path(os.environ.get('ANDROID_SDK_ROOT', os.environ.get('ANDROID_HOME', str(dev / 'tools/Android')))))
parser.add_argument('--java-home', type=Path, default=Path(os.environ.get('JAVA_HOME', str(dev / 'tools/jdk17'))))
parser.add_argument('--product-classes', type=Path, default=repo / 'server/build/intermediates/javac/release/compileReleaseJavaWithJavac/classes')
parser.add_argument('--product-classpath', type=Path, help='D8 classpath jar; defaults to product classes')
args = parser.parse_args()
sdk = args.sdk_root / 'platforms/android-36/android.jar'
base = args.product_classes
out = folder / 'out'
classes = out / 'classes'
classes.mkdir(parents=True, exist_ok=True)
assert (base / 'com/genymobile/scrcpy/NativeAlphaProbe.class').exists(), 'Build server first'
sources = list(folder.glob('*.java')) + [repo / 'research/performance-portability-20261101/SceneContext.java']
subprocess.run([str(args.java_home / ('bin/javac.exe' if os.name == 'nt' else 'bin/javac')), '-encoding', 'UTF-8', '-source', '11', '-target', '11', '-cp', str(sdk) + os.pathsep + str(base), '-d', str(classes), *map(str, sources)], check=True)
subprocess.run([str(args.java_home / ('bin/java.exe' if os.name == 'nt' else 'bin/java')), '-cp', str(args.sdk_root / 'build-tools/36.0.0/lib/d8.jar'), 'com.android.tools.r8.D8', '--min-api', '29', '--lib', str(sdk), '--classpath', str(args.product_classpath or base), '--output', str(out / 'probes.zip'), *map(str, classes.rglob('*.class'))], check=True)
print(out / 'probes.zip')
