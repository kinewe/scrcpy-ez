"""A normal Back action must invalidate the capture, with no false success."""
from pathlib import Path
import json, re, subprocess, sys, time
from device_tools import ADB, select_serial

folder=Path(__file__).resolve().parent
phone_log=folder/'out/wire-com.tencent.wework-leave-camera.log'
host_log=folder/'out/leave-camera-host.log'
with host_log.open('wb') as output:
    process=subprocess.Popen([sys.executable,str(folder/'run-wire-benchmark.py'),'com.tencent.wework','600','leave-camera'],stdout=output,stderr=subprocess.STDOUT)
    try:
        deadline=time.monotonic()+20
        while True:
            status=phone_log.read_text(encoding='utf8',errors='replace') if phone_log.exists() else ''
            if 'FILTERED frame=2 ' in status:break
            if process.poll() is not None or time.monotonic()>deadline:raise RuntimeError(status)
            time.sleep(.1)
        display=re.search(r'TARGET package=com\.tencent\.wework .*? display=(\d+)',status)[1]
        subprocess.run([ADB,'-s',select_serial(),'shell','input','-d',display,'keyevent','4'],check=True,timeout=10)
        process.wait(timeout=25)
    finally:
        if process.poll() is None:process.terminate()
status=phone_log.read_text(encoding='utf8',errors='replace')
host=host_log.read_text(encoding='utf8',errors='replace')
received=int(re.search(r'received=(\d+)/600',host)[1])
assert process.returncode!=0 and 0<received<600
assert 'transparent source validation lost' in status and 'PROBE_RESOURCES_RELEASED' in status
report={'case':'Normal Back while original scanner capture is active','expectedInvalidation':'PASS',
        'verifiedFramesBeforeInvalidation':received,'falseSuccess':False,'probeResourcesReleased':True}
(folder/'out/lifecycle.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(report,ensure_ascii=False))
