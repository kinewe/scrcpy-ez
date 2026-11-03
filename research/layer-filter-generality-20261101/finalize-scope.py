raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

"""Create a compact, hash-checked evidence bundle; omit full third-party binaries."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import hashlib, json, re, shutil, zipfile

folder=Path(__file__).resolve().parent;out=folder/'out';root=folder.parents[1]
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
builder=out/'k80-smali/android/window/ScreenCapture$CaptureArgs$Builder.smali'
capture=out/'k80-smali/android/window/ScreenCapture$CaptureArgs.smali'
snippets=['Extracted from K80 framework.jar; this is disassembly, not OEM C++ source.', 'framework SHA256: '+json.loads((out/'k80-framework-metadata.json').read_text())['frameworkSHA256']]
for path in [builder,capture]:
    lines=path.read_text(encoding='utf8').splitlines();snippets.append('\nCLASS '+path.name)
    i=0
    while i<len(lines):
        line=lines[i]
        if line.startswith('.field') and any(s in line for s in ['mCaptureMode','mExcludeOrIncludeLayerNames']):snippets.append(f'{i+1}: {line}')
        if line.startswith('.method') and any(s in line for s in ['setCaptureMode(','setExcludeOrIncludeLayerNames(','getNativeExcludeOrIncludeLayerNames(']):
            j=i
            while lines[j]!='.end method':j+=1
            snippets.extend(f'{k+1}: {lines[k]}' for k in range(i,j+1));i=j
        i+=1
(out/'oem-name-filter-evidence.txt').write_text('\n'.join(snippets)+'\n',encoding='utf8')
shutil.copyfile(research_config.build_dir() / 'scrcpy-server',out/'research-scrcpy-server.jar')
shutil.copyfile(folder.parent/'general-rgba-20261101/out/probes.zip',out/'baseline-original-probes.zip')
shutil.copyfile(folder.parent/'general-rgba-20261101/run-wire-benchmark.py',folder/'baseline-host-source-original.py')
shutil.copyfile(root/'docs/任务书_图层过滤泛用性调研.md',folder/'TASK.md')
runtime=Path(str(research_config.runtime()))
expected={'scrcpy.exe':'69791550274377D02CC52446B8F2749DAF07705FBFB16E5C9C6AF6897F522EEE','scrcpy-server':'FC18BEEA9926B0F25E83041CE398B000DA92BAF539A76E902EDAD157D869781A','scrcpy-ez.exe':'0224B3F7422E4AD373E523E78B22FD09A7A5F750DE7EC14757B12632D50144CC'}
# Explicit stable runtime hashes come from the previously delivered rc25 manifest.
runtime_hashes={n:sha(runtime/n).upper() for n in expected};assert runtime_hashes==expected,runtime_hashes
assert sha(out/'research-scrcpy-server.jar').upper()=='C96886946D75B149B2DA619AE46D04051186A3B575CC41623D61F2D247E62FDC'
for report in [folder/'REPORT.md',root/'docs/图层过滤泛用性调研报告_20261101.md']:
    for target in re.findall(r'\]\((?!https://)([^)]+)\)',report.read_text(encoding='utf8')):assert (report.parent/target).is_file(),target
files=[p for p in folder.iterdir() if p.is_file() and p.suffix in ['.java','.py','.ps1','.md','.patch']]
files.extend(p for p in out.rglob('*') if p.is_file() and (p.suffix in ['.json','.log','.txt'] or (p.name.startswith('aosp-') and p.suffix=='.java') or p.name in ['probes.zip','baseline-original-probes.zip','research-scrcpy-server.jar']))
files.extend(p for p in (folder/'before-product').iterdir() if p.is_file() and p.suffix in ['.java','.jar'])
files=sorted(set(files));assert not any(p.suffix in ['.apk','.dex'] or p.name in ['k80-framework.jar','tablet-framework.jar'] for p in files)
manifest=dict(task='图层名称过滤泛用性攻关',date='2026-11-01',runtimeUnchanged=runtime_hashes,sourceSnapshot='Final research snapshot includes power and CPU telemetry added after early profiles; early records without these fields are not retroactively filled.',results=json.loads((out/'results-summary.json').read_text(encoding='utf8')),cleanup=json.loads((out/'cleanup.json').read_text(encoding='utf8')),productProfilingSource={n:sha(root/'server/src/main/java/com/genymobile/scrcpy'/n) for n in ['NativeAlphaReadback.java','NativeAlphaFrameCodec.java']},files=[dict(path=p.relative_to(folder).as_posix(),bytes=p.stat().st_size,sha256=sha(p)) for p in files])
(folder/'MANIFEST.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf8')
archive=Path(str(research_config.archive_dir()/"rc25-layer-filter-generality-20261101.zip"))
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
    for p in files+[folder/'MANIFEST.json']:z.write(p,p.relative_to(folder).as_posix())
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None
    for entry in manifest['files']:assert hashlib.sha256(z.read(entry['path'])).hexdigest()==entry['sha256']
digest=sha(archive)
archive.with_suffix('.sha256').write_text(digest+'  '+archive.name+'\n',encoding='ascii')
print(json.dumps(dict(archive=str(archive),bytes=archive.stat().st_size,sha256=digest,files=len(files)+1,runtimeUnchanged=True)))
