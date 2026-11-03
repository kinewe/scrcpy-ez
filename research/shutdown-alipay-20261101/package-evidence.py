from pathlib import Path
import hashlib,json,os,shutil,zipfile
folder=Path(__file__).resolve().parent;root=folder.parents[1];out=folder/'out';record=out/'deployment.json'
report=json.loads(record.read_text(encoding='utf8'));runtime=Path(report['runtime']);sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest().upper()
doc=root/'docs/RC25_退出挂起修复与支付宝虚拟扫码_20261102.md';note='HOTFIX_20261102.md'
shutil.copy2(doc,runtime/note);shutil.copy2(doc,folder/'REPORT.md')
manifest=json.loads((runtime/'SOURCE_MANIFEST.json').read_text(encoding='utf8'));manifest['sha256'][note]=sha(runtime/note)
(runtime/'SOURCE_MANIFEST.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf8')
assert all(sha(runtime/n)==h for n,h in manifest['sha256'].items())
assert all(sha(runtime/n)==h for n,h in report['userConfigurationPreserved'].items())
patch=Path(report['patchZip']);assert sha(patch)==report['patchZipSHA256'];staged=patch.with_suffix('.zip.new');assert not staged.exists()
with zipfile.ZipFile(staged,'w',zipfile.ZIP_DEFLATED,6) as z:
    for name in ['scrcpy.exe','scrcpy-server','build.json','SOURCE_MANIFEST.json',note]:z.write(runtime/name,runtime.name+'/'+name)
with zipfile.ZipFile(staged) as z:assert z.testzip() is None
os.replace(staged,patch);report['patchZipSHA256']=sha(patch);record.write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
log=(out/'XHS-Exit.log').read_text(encoding='utf8',errors='replace');alipay=(out/'Alipay-Virtual.log').read_text(encoding='utf8',errors='replace')
assert 'NATIVE_ALPHA stopped after 842 verified frames' in log and 'NATIVE_ALPHA stopped after 0 verified frames' in alipay
for value in [log,alipay]:
    assert 'NATIVE_ALPHA_STOPPED restored=true' in value and 'SCRCPY_EZ_EXIT code=0' in value
    assert 'remote restore did not finish' not in value
assert 'PROCESS_INHERITANCE_RESULT failures=0' in (out/'fixed-process-test.log').read_text()
assert 'PROCESS_INHERITANCE_RESULT failures=3' in (out/'before-process-test.log').read_text()
for label in ['XHS-Exit','Alipay-Virtual']:
    cleanup=json.loads((out/(label+'-cleanup.json')).read_text());assert not cleanup['ownedServerOrAlphaProcesses'] and not cleanup['displayPresent'] and not cleanup['ownAlphaForward']
files={p.relative_to(folder).as_posix():sha(p) for p in folder.rglob('*') if p.is_file() and p.name!='MANIFEST.json' and '__pycache__' not in p.parts}
(folder/'MANIFEST.json').write_text(json.dumps(dict(filesSHA256=files),ensure_ascii=False,indent=2),encoding='utf8')
archive=runtime.parent/'rc25-shutdown-alipay-evidence-20261102.zip';assert not archive.exists()
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,6) as z:
    for p in sorted(folder.rglob('*')):
        if p.is_file() and '__pycache__' not in p.parts:z.write(p,folder.name+'/'+p.relative_to(folder).as_posix())
with zipfile.ZipFile(archive) as z:assert z.testzip() is None
print(json.dumps(dict(runtime=str(runtime),patchZip=str(patch),patchZipSHA256=report['patchZipSHA256'],evidenceZip=str(archive),evidenceZipSHA256=sha(archive),evidenceFiles=len(files)+1,manifestVerified=True),ensure_ascii=False))
