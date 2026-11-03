from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,re,subprocess,sys,time
label,serial,client,display,pkg=sys.argv[1:6]
assert label.replace('-','').isalnum();assert client.isdigit() and display.isdigit()
adb=research_config.adb()
def cmd(*args):return subprocess.check_output([adb,'-P',research_config.adb_port(),*args],timeout=20).decode('utf8',errors='replace')
def shell(*args):return cmd('-s',serial,'shell',*args)
ps=shell('ps','-A','-o','PID,ARGS');displays=shell('cmd','display','get-displays');forwards=cmd('forward','--list');whitelist=shell('cmd','deviceidle','whitelist')
owned=[s.strip() for s in ps.splitlines() if 'com.genymobile.scrcpy.Server' in s or 'NativeAlphaProbe' in s]
result=dict(epoch=time.time(),serial=serial,clientPid=int(client),displayId=int(display),ownedServerOrAlphaProcesses=owned,displayPresent=bool(re.search(r'displayId[= ]+'+display+r'\b',displays)),ownAlphaForward=[s for s in forwards.splitlines() if serial in s and 'ez_native_alpha_'+client in s],packageIdleWhitelist=[s for s in whitelist.splitlines() if pkg in s])
Path(__file__).with_name('out').joinpath(label+'-cleanup.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf8');print(json.dumps(result,ensure_ascii=False))
