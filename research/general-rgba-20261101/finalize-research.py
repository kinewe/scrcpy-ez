raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

"""Audit recorded experiments and package the standalone research prototype."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import hashlib, json, re, zipfile

folder=Path(__file__).resolve().parent
root=folder.parents[1]
out=folder/'out'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest().upper()
read=lambda p:p.read_text(encoding='utf8',errors='replace')
load=lambda name:json.loads(read(out/name))
records=[]
for stem,expected in [('wire-com.tencent.wework',600),('wire-com.valvesoftware.android.steam.community',240),('wire-com.tencent.wework-reentry',1800)]:
    record=load(stem+'.json');log=read(out/(stem+'.log'))
    assert record['frames']==expected and record['resolution']==[1440,3200]
    assert record['allFrameRGBA_CRC']=='PASS' and record['lastAlphaMin']<255 and record['lastAlphaMax']>0
    assert 'SOURCE_UNMODIFIED=true viewSetters=0 activityConversions=0 backgroundMutations=0' in log
    assert 'PROBE_RESOURCES_RELEASED' in log and 'alphaExact=true' in log
    record['cameraLayerId']=int(re.search(r'cameraLayer=.*?#(\d+)',log)[1])
    record['distinctRgbaCRC']=int(re.search(r'distinctRgbaCRC=(\d+)',log)[1])
    record['log']=stem+'.log'
    records.append(record)
assert records[0]['cameraLayerId']!=records[2]['cameraLayerId']
assert records[0]['distinctRgbaCRC']>1 and records[2]['distinctRgbaCRC']>1
self_log=read(out/'NameFilterLab.log')
for case in ['EXCLUDE_COMPLETE_SUBTREE','INCLUDE_UI_ONLY']:
    assert re.search(case+r' .*?rgbaMismatch=0(?:\r?$|\n)',self_log,re.M)
assert 'OWNED_LAYERS_CLEANED' in self_log
negative=load('negative-main.json');lifecycle=load('lifecycle.json');cleanup=load('cleanup.json')
assert negative['expectedRejection']=='PASS' and negative['receivedFrames']==0
assert lifecycle['expectedInvalidation']=='PASS' and not lifecycle['falseSuccess']
assert cleanup['ownedProcessesRemaining']==0
assert 'SCRCPY_EZ_EXIT code=0' in read(out/'resumed-window.log')

build=json.loads(read(root/'test-results/rc25/verification.json'))
runtime=Path(str(research_config.runtime()))
for name,digest in build['filesSHA256'].items():assert sha(runtime/name)==digest,name
product_zip=runtime.parent/'scrcpy-ez-2.4.2-rc.25-win64.zip'
assert sha(product_zip)=='0A356AEB5CDA72EA392A97F3201E73B0A444B2E5D38ECA0817D9BFDDA8752C13'
report={'date':'2026-11-01','status':'standalone-prototype-validated','productionIntegrated':False,
    'capability':'vendor layer-name exclusion of complete camera subtree, display mirror and UID scope',
    'rootUsed':False,'targetApkModified':False,'viewSetters':0,'activityTransparencyConversions':0,
    'device':{'model':'REDMI K80 / 24117RK2CC','sdk':36,
      'fingerprint':'Redmi/zorn/zorn:16/BP2A.250605.031.A3/OS3.0.309.0.WOKCNXM:user/release-keys'},
    'apps':{'com.tencent.wework':'5.0.11 / 81980','com.valvesoftware.android.steam.community':'3.10.11 / 10928360'},
    'completeFramesVerified':sum(x['frames'] for x in records),'runs':records,
    'ownedLayerPixelComparison':'PASS: 256-level A8 ramp and full RGBA, zero mismatches',
    'gpuAlphaExact':True,'mirrorRgbByteExact':False,'maxRgbConversionError':1,
    'negativeMainPage':negative,'leaveCamera':lifecycle,'cleanup':cleanup,
    'productionRc25BinariesUnchanged':True,'productionRc25ArchiveUnchanged':True,
    'productionFilesSHA256':build['filesSHA256'],'productionArchiveSHA256':sha(product_zip),
    'limits':['Verified on one ROM only; semantic self-test required on each implementation',
      'Independent SurfaceView preview required; shared TextureView/GLES/Vulkan not solved',
      'Prototype rates exclude GUI rendering; measurements are not a controlled speedup comparison',
      'App UID filtering does not include system IME or other UIDs']}
(out/'verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')

files=[p for p in folder.iterdir() if p.is_file() and p.suffix in ('.py','.java','.md')]
names=['GraphicsCapabilitiesProbe.log','NameFilterLab.log','wecom-window-root.log','wecom-display-mirror.log',
    'steam-display-mirror.log','negative-main.json','lifecycle.json','cleanup.json','verification.json','probes.zip']
for stem in ['wire-com.tencent.wework','wire-com.valvesoftware.android.steam.community','wire-com.tencent.wework-reentry']:
    names.extend([stem+'.log',stem+'.json'])
names.extend(['wire-com.tencent.wework-negative-main.log','wire-com.tencent.wework-leave-camera.log'])
files.extend(out/name for name in names)
manifest={p.relative_to(folder).as_posix():sha(p) for p in files}
(folder/'RESEARCH_MANIFEST.json').write_text(json.dumps({'sha256':manifest},ensure_ascii=False,indent=2),encoding='utf8')
files.append(folder/'RESEARCH_MANIFEST.json')
archive=runtime.parent/'rc25-general-rgba-research-20261101.zip'
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,6) as z:
    for p in sorted(files):z.write(p,'general-rgba-20261101/'+p.relative_to(folder).as_posix())
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None
    for name,digest in manifest.items():assert hashlib.sha256(z.read('general-rgba-20261101/'+name)).hexdigest().upper()==digest
print(json.dumps({'status':report['status'],'completeFramesVerified':report['completeFramesVerified'],
    'productionUnchanged':True,'archive':str(archive),'archiveSHA256':sha(archive),'files':len(files)},ensure_ascii=False))
