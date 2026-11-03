"""Run two owned, deadline-bounded app_process peers; never select a target app."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess,time,uuid,json,sys
folder=Path(__file__).resolve().parent;out=folder/'out'
adb=research_config.adb()
serial=sys.argv[1] if len(sys.argv)>1 else research_config.serial()
label=sys.argv[2] if len(sys.argv)>2 else 'k80';assert label.replace('-','').isalnum()
action='com.genymobile.scrcpy.rc25.BRIDGE.'+uuid.uuid4().hex
command=[adb,'-P',research_config.adb_port(),'-s',serial,'shell','CLASSPATH=/data/local/tmp/rc25-renderbridge-probes.zip:/data/local/tmp/rc25-renderbridge-base.jar','app_process','/','com.genymobile.scrcpy.SurfaceBridgeProbe']
consumer_log=out/('surface-bridge-'+label+'-consumer.log');producer_log=out/('surface-bridge-'+label+'-producer.log')
assert not consumer_log.exists() and not producer_log.exists(),'unique run label required'
with consumer_log.open('wb') as consumer_stream,producer_log.open('wb') as producer_stream:
    consumer=subprocess.Popen([*command,'consumer',action],stdout=consumer_stream,stderr=subprocess.STDOUT)
    try:
        deadline=time.monotonic()+8
        while 'BRIDGE_CONSUMER_READY' not in consumer_log.read_text(errors='replace'):
            if consumer.poll() is not None or time.monotonic()>deadline:raise RuntimeError(consumer_log.read_text(errors='replace'))
            time.sleep(.1)
        producer=subprocess.run([*command,'producer',action],stdout=producer_stream,stderr=subprocess.STDOUT,timeout=22)
        consumer.wait(timeout=28)
        print(json.dumps(dict(serial=serial,action=action,consumerExit=consumer.returncode,producerExit=producer.returncode)))
    finally:
        if consumer.poll() is None:
            # Device process also has its own 35-second watchdog; no broad kill.
            try:consumer.wait(timeout=35)
            except subprocess.TimeoutExpired:consumer.terminate();consumer.wait(timeout=5)
print(consumer_log.read_text(errors='replace'))
print(producer_log.read_text(errors='replace'))
