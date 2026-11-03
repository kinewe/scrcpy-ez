"""Bounded, read-only window experiment under controlled camera input fields."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess,json,time,re,sys
folder=Path(__file__).resolve().parent;out=folder/'out'
adb=research_config.adb()
display=int(sys.argv[1]);assert display>0
log=out/'dsh-composite-foundation-cast.log';control=out/'composite-control.txt'
assert log.exists() and control.exists()
offset=log.stat().st_size;limit=time.monotonic()+10
while not re.search(r'Perspective requests:.*rate=[1-9][0-9]*fps',log.read_bytes()[offset:].decode(errors='replace')):
    if time.monotonic()>limit:raise SystemExit('NO_FRESH_CAMERA_REQUESTS: no samples')
    time.sleep(.2)
results=[];begin=time.monotonic()
for phase,mode in [('black',3),('white',5),('gray',4),('red',6),('repeatblack',3)]:
    assert time.monotonic()-begin<180,'experiment deadline'
    control.write_text(str(mode),encoding='ascii')
    limit=time.monotonic()+5
    while 'PERSPECTIVE_TEST input_mode='+str(mode) not in log.read_text(errors='replace'):
        if time.monotonic()>limit:raise SystemExit('NO_INPUT_MODE_ACK '+phase)
        time.sleep(.2)
    time.sleep(1)
    label='composite-'+phase
    subprocess.run([sys.executable,str(folder/'run-dsh-probe.py'),label,'CompositeResponseProbe',str(display),phase,'3','--adb-port',research_config.adb_port(),'--serial',research_config.serial()],check=True,timeout=35)
    text=(out/('dsh-'+label+'.log')).read_text(errors='replace')
    data=json.loads(next(s.split('COMPOSITE_RESPONSE_JSON ',1)[1] for s in text.splitlines() if s.startswith('COMPOSITE_RESPONSE_JSON ')))
    for frame in data['frames']:
        local=out/Path(frame['path']).name
        subprocess.run([adb,'-P',research_config.adb_port(),'-s',research_config.serial(),'pull',frame['path'],str(local)],check=True,timeout=20,capture_output=True)
    results.append(data)
    print('PHASE_SAVED',phase,flush=True)
(out/'composite-response-index.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf8')
control.write_text('3',encoding='ascii')
print('ALL_PHASES_SAVED seconds='+str(round(time.monotonic()-begin,2)),flush=True)
