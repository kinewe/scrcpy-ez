"""Controlled pipeline experiment over a selected ADB transport."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import hashlib, json, socket, statistics, struct, subprocess, sys, time, uuid
import zlib

folder=Path(__file__).resolve().parent;root=folder.parents[1]
label,serial,pkg,mode=sys.argv[1:5];count=int(sys.argv[5]);delay=float(sys.argv[6]) if len(sys.argv)>6 else 0
awake=len(sys.argv)>7 and sys.argv[7]=='awake'
assert label.replace('-','').isalnum() and mode in ('capture','encode','wire')
adb=research_config.adb();port=research_config.adb_port()
def adb_out(*args):return subprocess.check_output([adb,'-P',port,'-s',serial]+list(args),timeout=30).decode('utf8',errors='replace').strip()
for source,target in [(folder/'out/probes.zip','rc25-scope-probes.zip'),(research_config.build_dir() / 'scrcpy-server','rc25-scope-base.jar')]:adb_out('push',str(source),'/data/local/tmp/'+target)
name='rc25_scope_'+uuid.uuid4().hex;forward=None;process=None;received=0;host_decode=[];arrivals=[]
command=[adb,'-P',port,'-s',serial,'shell','CLASSPATH=/data/local/tmp/rc25-scope-probes.zip:/data/local/tmp/rc25-scope-base.jar','app_process','/','com.genymobile.scrcpy.PipelineProbe',pkg,str(count),mode]
if awake:command.insert(6,'RC25_RESEARCH_WAKE=1')
def exact(conn,length):
    result=bytearray()
    while len(result)<length:
        part=conn.recv(length-len(result))
        if not part:raise EOFError('truncated stream')
        result.extend(part)
    return result
logfile=folder/'out'/(label+'.log')
try:
    if mode=='wire':forward=int(adb_out('forward','tcp:0','localabstract:'+name));command.append(name)
    with logfile.open('wb') as log:
        process=subprocess.Popen(command,stdout=log,stderr=subprocess.STDOUT)
        if mode=='wire':
            deadline=time.monotonic()+20
            while b'WIRE_LISTENING' not in logfile.read_bytes():
                if process.poll() is not None or time.monotonic()>deadline:raise RuntimeError(logfile.read_text(errors='replace'))
                time.sleep(.05)
            with socket.create_connection(('127.0.0.1',forward),timeout=3) as conn:
                conn.settimeout(20);frame=None
                while True:
                    magic=struct.unpack('>I',exact(conn,4))[0]
                    if magic==0x52474c30:break
                    assert magic==0x52474c31
                    w,h,seq,flags,size,crc,raw_size=struct.unpack('>7I',exact(conn,28));assert seq==received+1 and raw_size==w*h*4 and size<=raw_size+1048576
                    packed=exact(conn,size);at=time.perf_counter_ns();raw=zlib.decompress(packed)
                    if flags==2:assert len(raw)==raw_size;frame=bytearray(raw)
                    else:
                        assert flags==3 and frame is not None
                        changes=struct.unpack_from('>I',raw)[0];offset=4
                        for _ in range(changes):
                            start,length=struct.unpack_from('>II',raw,offset);offset+=8;assert start+length<=len(frame) and offset+length<=len(raw);frame[start:start+length]=raw[offset:offset+length];offset+=length
                        assert offset==len(raw)
                    assert zlib.crc32(frame)==crc
                    host_decode.append((time.perf_counter_ns()-at)/1e6);arrivals.append(time.monotonic());received+=1
                    if delay:time.sleep(delay/1000)
        process.wait(timeout=600)
    log=logfile.read_text(encoding='utf8',errors='replace');assert process.returncode==0,log[-6000:]
    report=json.loads(next(s[len('PROFILE_JSON '):] for s in log.splitlines() if s.startswith('PROFILE_JSON ')))
    report['context']=json.loads(next(s[len('PIPELINE_CONTEXT '):] for s in log.splitlines() if s.startswith('PIPELINE_CONTEXT ')))
    report['label']=label;report['adbTransport']='tls-mdns' if '_adb-tls-' in serial else 'tcp-5555' if ':5555' in serial else 'usb-or-other'
    report['probeSHA256']=hashlib.sha256((folder/'out/probes.zip').read_bytes()).hexdigest()
    report['serverSHA256']=hashlib.sha256((research_config.build_dir() / 'scrcpy-server').read_bytes()).hexdigest()
    report['hostReceiveDelayMs']=delay
    report['requestedScreenWakeLock']=awake
    if mode=='wire':
        assert received==count
        report['verifiedWireFrames']=received;report['hostReceiveFps']=(received-1)/(arrivals[-1]-arrivals[0]);report['hostDecodeCrcP50Ms']=statistics.median(host_decode)
    (folder/'out'/(label+'.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
    print(json.dumps({'label':label,'mode':mode,'frames':count,'loopFps':report['loopFps'],'hostFps':report.get('hostReceiveFps'),'stageP50Ms':{k:round(v['p50Ms'],3)for k,v in report['stages'].items()},'memorySamples':len(report['memory'])},ensure_ascii=False),flush=True)
finally:
    if process is not None and process.poll() is None:process.terminate()
    if forward is not None:adb_out('forward','--remove','tcp:'+str(forward))
