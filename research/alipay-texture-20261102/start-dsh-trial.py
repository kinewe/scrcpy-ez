from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import os,subprocess,json,time,sys
from datetime import datetime, timezone
folder=Path(__file__).resolve().parent;out=folder/'out';runtime=Path(str(research_config.runtime()));base=Path(str(research_config.build_dir()))
port=sys.argv[2] if len(sys.argv)>2 else research_config.adb_port();serial=sys.argv[3] if len(sys.argv)>3 else research_config.serial();assert port.isdigit()
package=sys.argv[4] if len(sys.argv)>4 else 'com.eg.android.AlipayGphone';assert package in ['com.eg.android.AlipayGphone','com.android.shell']
env=os.environ.copy();env.update(ADB=str(runtime/'adb.exe'),ADB_SERVER_SOCKET='tcp:localhost:'+port,SCRCPY_SERVER_PATH=str(base/'scrcpy-server'),PATH=str(runtime)+os.pathsep+str(research_config.mingw()/"bin")+os.pathsep+env.get('PATH',''))
label=sys.argv[1] if len(sys.argv)>1 else 'live';assert label.replace('-','').isalnum()
max_seconds=int(os.environ.get('RC25_TRIAL_SECONDS','300'));assert 1<=max_seconds<=600
deadline_text=os.environ.get('RC25_TRIAL_DEADLINE_UTC')
if deadline_text:
    deadline=datetime.fromisoformat(deadline_text.replace('Z','+00:00'));assert deadline.tzinfo is not None
    max_seconds=min(max_seconds,int((deadline-datetime.now(timezone.utc)).total_seconds()))
assert max_seconds>0,'trial deadline already passed'
log=out/('dsh-'+label+'-cast.log');assert not log.exists(),'unique trial log required'
with log.open('wb') as stream:
    p=subprocess.Popen([str(out/'dsh-client/scrcpy-external-alpha.exe'),'-s',serial,'--new-display=1440x3200/600','--start-app='+package,'--no-audio','--max-fps=30','--video-bit-rate=12M','--window-width=520','--window-height=1156','--window-title=RC25 Temporal Alpha Trial','--perspective-camera','--perspective-input-fps=1'],env=env,stdout=stream,stderr=subprocess.STDOUT)
    (out/('dsh-'+label+'-client.json')).write_text(json.dumps(dict(pid=p.pid,epoch=time.time(),log=str(log),adbPort=int(port),serial=serial)),encoding='utf8')
    print('CLIENT_STARTED',p.pid,flush=True)
    try:
        result=p.wait(timeout=max_seconds);print('CLIENT_EXIT',result,flush=True)
    except subprocess.TimeoutExpired:
        print('CLIENT_TIME_LIMIT_TERMINATE',p.pid,flush=True)
        p.terminate()
        try: p.wait(timeout=10)
        except subprocess.TimeoutExpired: p.kill();p.wait(timeout=5)
        print('CLIENT_FORCED_EXIT',p.returncode,flush=True)
