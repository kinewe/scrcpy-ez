"""Bounded public-NDK handle IPC between two owned shell processes."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess,time,uuid,json,sys
folder=Path(__file__).resolve().parent;out=folder/'out'
adb=research_config.adb()
serial=sys.argv[1] if len(sys.argv)>1 else research_config.serial();label=sys.argv[2] if len(sys.argv)>2 else 'k80';assert label.replace('-','').isalnum()
name='rc25_buffer_'+uuid.uuid4().hex
command=[adb,'-P',research_config.adb_port(),'-s',serial,'shell','CLASSPATH=/data/local/tmp/rc25-renderbridge-probes.zip:/data/local/tmp/rc25-renderbridge-base.jar','app_process','/','com.genymobile.scrcpy.BufferBridgeProbe']
producer_log=out/('buffer-bridge-'+label+'-producer.log');consumer_log=out/('buffer-bridge-'+label+'-consumer.log');assert not producer_log.exists() and not consumer_log.exists()
with producer_log.open('wb') as source,consumer_log.open('wb') as sink:
 producer=subprocess.Popen([*command,'producer',name],stdout=source,stderr=subprocess.STDOUT)
 try:
  deadline=time.monotonic()+8
  while 'BUFFER_BRIDGE_READY' not in producer_log.read_text(errors='replace'):
   if producer.poll() is not None or time.monotonic()>deadline:raise RuntimeError(producer_log.read_text(errors='replace'))
   time.sleep(.1)
  consumer=subprocess.run([*command,'consumer',name],stdout=sink,stderr=subprocess.STDOUT,timeout=35)
  producer.wait(timeout=15)
  print(json.dumps(dict(serial=serial,consumerExit=consumer.returncode,producerExit=producer.returncode)))
 finally:
  if producer.poll() is None:
   try:producer.wait(timeout=40)
   except subprocess.TimeoutExpired:producer.terminate();producer.wait(timeout=5)
print(producer_log.read_text(errors='replace'));print(consumer_log.read_text(errors='replace'))
