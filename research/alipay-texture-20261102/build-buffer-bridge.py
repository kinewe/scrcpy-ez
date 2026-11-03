"""Freestanding ARM64 JNI build. Compiler and device libraries stay in out/."""
from pathlib import Path
import subprocess,zipfile,hashlib,json,os
folder=Path(__file__).resolve().parent;out=folder/'out';tools=out/'native-tools'
wheel=tools/'ziglang-0.16.0-py3-none-win_amd64.whl';zig=tools/'unpacked/ziglang/zig.exe'
if not zig.exists():
    with zipfile.ZipFile(wheel) as archive:
        root=(tools/'unpacked').resolve()
        assert all((root/name).resolve().is_relative_to(root) for name in archive.namelist())
        archive.extractall(root)
env=os.environ.copy();env['ZIG_GLOBAL_CACHE_DIR']=str(out/'zig-cache-global');env['ZIG_LOCAL_CACHE_DIR']=str(out/'zig-cache-local')
temporary=out/'native-temp';temporary.mkdir(exist_ok=True)
for key in ['TEMP','TMP','TMPDIR']:env[key]=str(temporary)
library=out/'librc25_bufferbridge.so'
subprocess.run([str(zig),'cc','-target','aarch64-linux-android','-fuse-ld=lld','-shared','-nostdlib','-fno-stack-protector','-fno-builtin','-O2','-Wl,-soname,librc25_bufferbridge.so','-Wl,-z,max-page-size=16384','-Wl,--allow-shlib-undefined',str(folder/'buffer-bridge.c'),str(out/'libandroid-device.so'),str(out/'libnativewindow-device.so'),'-o',str(library)],env=env,check=True,timeout=90)
metadata=dict(compilerWheel=wheel.name,compilerWheelSha256=hashlib.sha256(wheel.read_bytes()).hexdigest(),librarySha256=hashlib.sha256(library.read_bytes()).hexdigest(),libraryBytes=library.stat().st_size)
(out/'buffer-bridge-build.json').write_text(json.dumps(metadata,indent=2));print(json.dumps(metadata))
