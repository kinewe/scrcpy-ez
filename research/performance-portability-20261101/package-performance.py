raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import hashlib,json,re,shutil,zipfile
folder=Path(__file__).resolve().parent;root=folder.parents[1];out=folder/'out'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
candidate=out/'rc25-performance-server.jar';shutil.copyfile(research_config.build_dir() / 'scrcpy-server',candidate)
assert sha(candidate)=='0afef919793866933d0cafd1e27612134b17c55153d5a41e1c12ca1fe9b114f5'
assert sha(root/'server/src/main/java/com/genymobile/scrcpy/NativeAlphaReadback.java')==sha(folder/'before-product/NativeAlphaReadback.java')
shutil.copyfile(root/'server/src/main/java/com/genymobile/scrcpy/NativeAlphaFrameCodec.java',out/'NativeAlphaFrameCodec-optimized.java')
runtime=Path(str(research_config.runtime()))
expected={'scrcpy.exe':'69791550274377d02cc52446b8f2749daf07705fbfb16e5c9c6af6897f522eee','scrcpy-server':'fc18beea9926b0f25e83041ce398b000da92baf539a76e902edad157d869781a','scrcpy-ez.exe':'0224b3f7422e4ad373e523e78b22fd09a7a5f750de7ec14757b12632d50144cc'}
actual={n:sha(runtime/n)for n in expected};assert actual==expected,actual
for p in [folder/'REPORT.md',root/'docs/RC25_性能与近期ROM调研_20261101.md']:
    for target in re.findall(r'\]\((?!https://)([^)]+)\)',p.read_text(encoding='utf8')):assert (p.parent/target).is_file(),target
files=[p for p in folder.iterdir() if p.is_file() and p.suffix in ['.py','.java','.ps1','.md','.patch']]
files.extend(p for p in out.rglob('*') if p.is_file() and (p.suffix in ['.json','.log'] or p.name in ['probes.zip','rc25-performance-server.jar','NativeAlphaFrameCodec-optimized.java']))
files.extend(p for p in (folder/'before-product').iterdir() if p.is_file() and p.suffix in ['.java','.jar'])
files=sorted(set(files));assert not any(p.suffix in ['.apk','.dex'] for p in files)
# Third-party decompiled classes remain local. Deliver URLs, hashes and findings only.
assert not any('modern-sources' in p.parts and p.suffix=='.java' for p in files)
manifest=dict(date='2026-11-01',task='RC25性能、结构分类和近期厂商API调研',runtimeUnchanged=actual,candidateServerSHA256=sha(candidate),readbackOptimizationReverted=True,sourceSnapshot='Final helper snapshot includes later read-only capability inventory additions; historical profile SHA fields retain their actual values.',summary=json.loads((out/'summary.json').read_text(encoding='utf8')),cleanup=json.loads((out/'cleanup.json').read_text(encoding='utf8')),exitCleanup=json.loads((out/'exit-hang.json').read_text(encoding='utf8')),files=[dict(path=p.relative_to(folder).as_posix(),bytes=p.stat().st_size,sha256=sha(p))for p in files])
(folder/'MANIFEST.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf8')
archive=Path(str(research_config.archive_dir()/"rc25-performance-portability-20261101.zip"))
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
    for p in files+[folder/'MANIFEST.json']:z.write(p,p.relative_to(folder).as_posix())
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None
    for entry in manifest['files']:assert hashlib.sha256(z.read(entry['path'])).hexdigest()==entry['sha256']
digest=sha(archive);archive.with_suffix('.sha256').write_text(digest+'  '+archive.name+'\n',encoding='ascii')
print(json.dumps(dict(archive=str(archive),sha256=digest,bytes=archive.stat().st_size,files=len(files)+1,runtimeUnchanged=True)))
