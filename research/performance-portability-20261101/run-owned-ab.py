from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,os,subprocess,sys
folder=Path(__file__).resolve().parent;root=folder.parents[1];serial=research_config.serial()
baseline=folder/'before-product/scrcpy-server.jar';optimized=research_config.build_dir() / 'scrcpy-server'
cases=[('codec-b2','CodecBenchmarkProbe',optimized,[]),('codec-a2','CodecBenchmarkProbe',baseline,[]),('readback-a-1440','SyntheticReadbackProbe',baseline,['1440']),('readback-a-1441','SyntheticReadbackProbe',baseline,['1441']),('readback-b-1440','SyntheticReadbackProbe',optimized,['1440']),('readback-b-1441','SyntheticReadbackProbe',optimized,['1441'])]
for label,klass,server,args in cases:
    subprocess.run([sys.executable,str(folder/'run-probe.py'),label,serial,klass]+args,env=dict(os.environ,RC25_PERF_SERVER=str(server)),check=True)
codec=[]
for label in ['codec-a1','codec-b1','codec-b2','codec-a2']:
    data=(folder/'out'/f'{label}-CodecBenchmarkProbe.log').read_text(encoding='utf8');r=json.loads(next(s.split('CODEC_JSON ',1)[1] for s in data.splitlines() if s.startswith('CODEC_JSON ')));r['label']=label;codec.append(r)
assert len({(r['packedStreamCRC'],r['packedBytes'],r['inputBytes'])for r in codec})==1
readback=[]
for width in [1440,1441]:
    pair=[]
    for variant in ['a','b']:
        label=f'readback-{variant}-{width}';data=(folder/'out'/f'{label}-SyntheticReadbackProbe.log').read_text(encoding='utf8');r=json.loads(next(s.split('SYNTHETIC_JSON ',1)[1] for s in data.splitlines() if s.startswith('SYNTHETIC_JSON ')));r['label']=label;readback.append(r);pair.append(r)
    assert pair[0]['crc']==pair[1]['crc'] and all(r['allFramesByteExact'] for r in pair)
(folder/'out/owned-ab.json').write_text(json.dumps(dict(codec=codec,readback=readback),indent=2),encoding='utf8');print('OWNED_AB_ALL_OUTPUTS_IDENTICAL')
