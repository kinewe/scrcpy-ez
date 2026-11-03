raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

"""Deploy only verified rc25 binaries; preserve user data and original release ZIP."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import difflib,hashlib,json,os,shutil,subprocess,zipfile
folder=Path(__file__).resolve().parent;root=folder.parents[1];build=research_config.build_dir()
parent=Path(str(research_config.runtime().parent));runtime=parent/'scrcpy-ez-2.4.2-rc.25';backup=parent/'rc25-pre-shutdownfix-20261102'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest().upper()
expected={'scrcpy.exe':'0692C539D9BF1E19CB169DC863F1032A84EDB3F7FED260274751C423E1C7577A','scrcpy-server':'0AFEF919793866933D0CAFD1E27612134B17C55153D5A41E1C12CA1FE9B114F5'}
old={'scrcpy.exe':'69791550274377D02CC52446B8F2749DAF07705FBFB16E5C9C6AF6897F522EEE','scrcpy-server':'FC18BEEA9926B0F25E83041CE398B000DA92BAF539A76E902EDAD157D869781A'}
assert not backup.exists(),backup
for name,value in expected.items():assert sha(build/name)==value and sha(runtime/name)==old[name]
gui=sha(runtime/'scrcpy-ez.exe');assert gui=='0224B3F7422E4AD373E523E78B22FD09A7A5F750DE7EC14757B12632D50144CC'
preserved={n:sha(runtime/n) for n in ['profiles.json','settings.json']}
metadata=json.loads((build/'build.json').read_text(encoding='utf8'));assert metadata['version']=='2.4.2-rc.25'
assert all(metadata['filesSHA256'][n]==h for n,h in {**expected,'scrcpy-ez.exe':gui}.items())
backup.mkdir();changed=['scrcpy.exe','scrcpy-server','build.json','SOURCE_MANIFEST.json']
for name in changed:shutil.copy2(runtime/name,backup/name)
for name in ['scrcpy.exe','scrcpy-server','build.json']:
    target=runtime/name;staged=target.with_name(name+'.rc25-new');assert not staged.exists();shutil.copy2(build/name,staged);os.replace(staged,target)
note='HOTFIX_20261102.md';assert not (runtime/note).exists();shutil.copy2(root/'docs/RC25_退出挂起修复与支付宝虚拟扫码_20261102.md',runtime/note)
manifest=json.loads((backup/'SOURCE_MANIFEST.json').read_text(encoding='utf8'))
manifest['hotfix']='20261102-shutdown-handle-inheritance-and-validated-codec-copy-removal'
for name in ['scrcpy.exe','scrcpy-server','build.json',note]:manifest['sha256'][name]=sha(runtime/name)
(runtime/'SOURCE_MANIFEST.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf8')
assert all(sha(runtime/n)==v for n,v in manifest['sha256'].items())
assert {n:sha(runtime/n) for n in preserved}==preserved and sha(runtime/'scrcpy-ez.exe')==gui
patch=parent/'rc25-shutdownfix-20261102.zip';assert not patch.exists()
with zipfile.ZipFile(patch,'w',zipfile.ZIP_DEFLATED,6) as z:
    for name in changed+[note]:z.write(runtime/name,runtime.name+'/'+name)
with zipfile.ZipFile(patch) as z:assert z.testzip() is None
after=folder/'after';after.mkdir(exist_ok=True)
rel='app/src/sys/win/process.c';dest=after/rel;dest.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(root/rel,dest)
diff=''.join(difflib.unified_diff((folder/'before'/rel).read_text().splitlines(True),(root/rel).read_text().splitlines(True),fromfile='before/'+rel,tofile=rel))
(folder/'product-shutdown.patch').write_text(diff,encoding='utf8')
shutil.copy2(root/'research/performance-portability-20261101/product-performance.patch',folder/'product-performance.patch')
shutil.copy2(root/'server/src/main/java/com/genymobile/scrcpy/NativeAlphaFrameCodec.java',after/'NativeAlphaFrameCodec.java')
shutil.copy2(root/'docs/RC25_退出挂起修复与支付宝虚拟扫码_20261102.md',folder/'REPORT.md')
version=subprocess.run([str(runtime/'scrcpy.exe'),'--version'],cwd=runtime,capture_output=True,text=True,timeout=15);assert version.returncode==0 and 'rc.25' in version.stdout
report=dict(runtime=str(runtime),backup=str(backup),filesSHA256=expected,guiSHA256=gui,userConfigurationPreserved=preserved,patchZip=str(patch),patchZipSHA256=sha(patch),versionSmoke=version.stdout.strip(),manifestVerified=True,originalReleaseZipPreserved=True,UI1Untouched=True)
(folder/'out/deployment.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8');print(json.dumps(report,ensure_ascii=False))
