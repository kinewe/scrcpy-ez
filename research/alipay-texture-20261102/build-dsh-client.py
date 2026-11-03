"""Local research client: rebuild one source copy, reuse verified rc25 objects."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import re,json,subprocess,os,hashlib,shutil
folder=Path(__file__).resolve().parent;repo=folder.parents[1]
product=Path(str(research_config.product()));base=research_config.build_dir();old=Path(str(research_config.reference_build()));out=folder/'out/dsh-client';out.mkdir(parents=True,exist_ok=True)
runtime=Path(str(research_config.runtime()))
os.environ['PATH']=str(research_config.mingw()/"bin")+os.pathsep+str(runtime)+';'+os.environ.get('PATH','')
text=(repo/'app/src/scrcpy.c').read_text(encoding='utf8');assert text==(product/'app/src/scrcpy.c').read_text(encoding='utf8')
needle='if (!sc_native_source_start(native_source_serial, target_package)) {';assert text.count(needle)==1
copy=out/'scrcpy-external-alpha.c';copy.write_text(text.replace(needle,'LOGI("RESEARCH: external alpha controller only");\n        if (false) {'),encoding='utf8')
split=lambda s:[a or b for a,b in re.findall(r'"([^\"]*)"|(\S+)',s)]
entries=json.loads((old/'compile_commands.json').read_text());by={Path(e['output']).name:e for e in entries};entry=next(e for e in entries if e['file'].replace('\\','/').endswith('/scrcpy.c'))
cmd=split(entry['command']);cmd[0]=research_config.gcc();cmd[-1]=str(copy);obj=out/'scrcpy-external-alpha.obj'
for opt in ['-o','-MF','-MQ']:
    if opt in cmd:cmd[cmd.index(opt)+1]=str(obj)+('.d' if opt=='-MF' else '')
cmd[1:1]=['-I'+str(base),'-I'+str(repo/'app/src'),'-I'+str(repo/'app'),"-I"+str(research_config.codec_prefix()/"include")]
subprocess.run(cmd,cwd=old,check=True)
lines=(old/'build.ninja').read_text().splitlines();idx=next(i for i,x in enumerate(lines) if x.startswith('build app/scrcpy.exe:'));objects=[];covered=set()
for name in lines[idx].split('c_LINKER_RSP ',1)[1].split(' | ',1)[0].split():
    e=by.get(Path(name).name)
    if not e:assert name.endswith('scrcpy-windows.o');objects.append(str(base/'scrcpy-windows-rc25.o'));continue
    rel=e['file'].replace('\\','/').split('/app/src/',1)[1];covered.add(rel);objects.append(str(obj if rel=='scrcpy.c' else base/Path(name).name))
extra=sorted(p.name for p in (product/'app/src').glob('*.c') if p.name not in covered and p.name not in {'v4l2_sink.c','vaapi_shim.c','gpu_matte.c','badge_layout.c'})
objects.extend(str(base/(name+'.obj')) for name in extra);assert all(Path(p).is_file() for p in objects)
link=split(next(x.split(' = ',1)[1] for x in lines[idx+1:] if x.startswith(' LINK_ARGS =')))
for i,arg in enumerate(link):
    if re.search(r'/lib(libavcodec|libavformat|libavutil|libswscale|libswresample)\.dll\.a$',arg):link[i]=str(research_config.codec_prefix()/"lib")+"/"+Path(arg).name
client=out/'scrcpy-external-alpha.exe';subprocess.run([cmd[0],'-o',str(client),*objects,*link,str(research_config.mingw()/"lib/libz.a")],cwd=old,check=True)
for name in ['scrcpy.png','disconnected.png']:shutil.copy2(runtime/name,out/name)
for name in ['calibration','icons']:shutil.copytree(runtime/name,out/name,dirs_exist_ok=True)
env=os.environ.copy();env['PATH']=str(runtime)+os.pathsep+str(research_config.mingw()/"bin")+os.pathsep+env.get('PATH','');p=subprocess.run([str(client),'--version'],env=env,capture_output=True);assert p.returncode==0,p.stderr
print(json.dumps(dict(client=str(client),sha256=hashlib.sha256(client.read_bytes()).hexdigest(),reusedObjectsFrom=str(base),externalController=True)))
