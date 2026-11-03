from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json, subprocess, sys, zipfile
folder=Path(__file__).resolve().parent;out=folder/'out';label=sys.argv[1]
meta=json.loads((out/(label+'-framework-metadata.json')).read_text(encoding='utf8'))
descriptors=['Landroid/window/ScreenCapture$CaptureArgs;','Landroid/window/ScreenCapture$CaptureArgs$Builder;']
dex_name=next(c['dex'] for c in meta['classes'] if c['class']==descriptors[0])
with zipfile.ZipFile(out/(label+'-framework.jar')) as z:(out/(label+'-'+dex_name)).write_bytes(z.read(dex_name))
lib=Path(str(research_config.sdk()/"cmdline-tools/latest/lib"))
jars=[p for p in lib.rglob('*.jar') if p.name.startswith(('smali-','guava-','jcommander-','failureaccess-'))]
command=[research_config.java_bin("java"),'-cp',';'.join(str(p) for p in jars),'com.android.tools.smali.baksmali.Main','disassemble',str(out/(label+'-'+dex_name)), '--classes',','.join(descriptors),'-o',str(out/(label+'-smali'))]
p=subprocess.run(command,capture_output=True,timeout=90)
(out/(label+'-baksmali.log')).write_bytes(p.stdout+p.stderr)
print((p.stdout+p.stderr).decode('utf8',errors='replace'));assert p.returncode==0
files=list((out/(label+'-smali')).rglob('*.smali'));assert len(files)==2
print('Disassembled exact capture argument classes:',len(files))
