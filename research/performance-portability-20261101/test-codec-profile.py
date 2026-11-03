from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess
folder=Path(__file__).resolve().parent;root=folder.parents[1];out=folder/'out/codec-parity';out.mkdir(exist_ok=True)
source=(folder/'before-product/NativeAlphaFrameCodec.java').read_text(encoding='utf8').replace('NativeAlphaFrameCodec','BaselineCodec')
(out/'BaselineCodec.java').write_text(source,encoding='utf8')
subprocess.run([research_config.java_bin("javac"),'-encoding','UTF-8','-d',str(out),str(out/'BaselineCodec.java'),str(root/'server/src/main/java/com/genymobile/scrcpy/NativeAlphaFrameCodec.java'),str(folder/'CodecProfilingParity.java')],check=True)
p=subprocess.run([research_config.java_bin("java"),'-cp',str(out),'com.genymobile.scrcpy.CodecProfilingParity'],capture_output=True,check=True)
(out/'result.log').write_bytes(p.stdout+p.stderr);print(p.stdout.decode())
