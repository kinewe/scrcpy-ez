from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,os,re,subprocess,sys
folder=Path(__file__).resolve().parent;root=folder.parents[1];old=Path(str(research_config.reference_build()));variant=sys.argv[1]
assert variant in ['before','fixed'];out=folder/'out';out.mkdir(exist_ok=True)
entry=next(e for e in json.loads((old/'compile_commands.json').read_text()) if e['file'].replace('\\','/').endswith('/sys/win/process.c'))
def split(text):return [a or b for a,b in re.findall(r'"([^\"]*)"|(\S+)',text)]
env=dict(os.environ,PATH=str(research_config.runtime())+os.pathsep+str(research_config.mingw()/"bin")+os.pathsep+os.environ['PATH'])
source=folder/'before/app/src/sys/win/process.c' if variant=='before' else root/'app/src/sys/win/process.c'
objects=[]
for label,file in [('process',source),('test',folder/'test_process_inheritance.c')]:
    target=out/(variant+'-'+label+'.obj');cmd=split(entry['command']);cmd[0]=research_config.gcc();cmd[-1]=str(file)
    for option in ['-o','-MF','-MQ']:
        if option in cmd:cmd[cmd.index(option)+1]=str(target)+('.d' if option=='-MF' else '')
    cmd[1:1]=['-I'+str(research_config.build_dir()),'-I'+str(root/'app/src'),'-I'+str(root/'app')]
    result=subprocess.run(cmd,cwd=old,env=env,capture_output=True);(out/(variant+'-'+label+'-compile.log')).write_bytes(result.stdout+result.stderr);assert result.returncode==0,result.stderr.decode(errors='replace');objects.append(str(target))
link=split(next(l.split(' = ',1)[1] for l in (old/'build.ninja').read_text().splitlines() if l.startswith(' LINK_ARGS = ')))
sdl=next(p for p in link if p.endswith('/libSDL3.dll.a'));exe=out/(variant+'-process-test.exe')
subprocess.run([research_config.gcc(),'-o',str(exe)]+objects+[sdl,'-lws2_32','-mconsole'],check=True,env=env)
result=subprocess.run([str(exe)],capture_output=True,env=env,timeout=15);(out/(variant+'-process-test.log')).write_bytes(result.stdout+result.stderr);print(result.stdout.decode(errors='replace'));print('PROCESS_TEST_EXIT',result.returncode)
if variant=='before':assert result.returncode!=0,'The baseline must reproduce the regression'
else:assert result.returncode==0,'The fixed implementation must pass'
