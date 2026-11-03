"""Receive the existing lossless RGBA codec over real ADB and verify every frame CRC."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json, socket, struct, subprocess, sys, time, uuid, zlib
import os
ADB=research_config.adb()
def select_serial():return research_config.serial()

folder=Path(__file__).resolve().parent
root=folder.parents[1]
adb=ADB
serial=select_serial()
pkg=sys.argv[1]
count=int(sys.argv[2]) if len(sys.argv)>2 else 180
assert pkg.replace('.','').replace('_','').isalnum() and 2<=count<=3000
name='rc25_named_'+uuid.uuid4().hex
def adb_run(*args):
    return subprocess.check_output([adb,"-P",research_config.adb_port()]+list(args),timeout=30).decode('utf8',errors='replace').strip()
print('Using online research device',flush=True)
adb_run('-s',serial,'push',str(folder/'out/baseline-original-probes.zip'),'/data/local/tmp/rc25-scope-baseline-probes.zip')
adb_run('-s',serial,'push',str(folder/'before-product/scrcpy-server.jar'),'/data/local/tmp/rc25-scope-baseline-base.jar')
port=int(adb_run('-s',serial,'forward','tcp:0','localabstract:'+name))
tag=sys.argv[3] if len(sys.argv)>3 else ''
assert not tag or tag.replace('-','').replace('_','').isalnum()
stem='wire-'+pkg+('-'+tag if tag else '')
proc=None
def exact(sock,size):
    data=bytearray()
    while len(data)<size:
        part=sock.recv(size-len(data))
        if not part:raise EOFError('Truncated packet')
        data.extend(part)
    return data
try:
    with (folder/'out'/(stem+'.log')).open('wb') as log:
        proc=subprocess.Popen([adb,'-P',research_config.adb_port(),'-s',serial,'shell',
            'CLASSPATH=/data/local/tmp/rc25-scope-baseline-probes.zip:/data/local/tmp/rc25-scope-baseline-base.jar',
            'app_process','/','com.genymobile.scrcpy.AppNameFilterProbe',pkg,str(count),'mirror',name],stdout=log,stderr=subprocess.STDOUT)
        until=time.monotonic()+15
        while True:
            status=(folder/'out'/(stem+'.log')).read_bytes()
            if b'WIRE_LISTENING' in status:
                conn=socket.create_connection(('127.0.0.1',port),timeout=2);break
            if time.monotonic()>until or proc.poll() is not None:raise RuntimeError(status.decode('utf8',errors='replace'))
            time.sleep(.05)
        received=0;total_packed=0;frame=None;first_at=last_at=0;first_alpha=None
        with conn:
            conn.settimeout(15)
            while True:
                magic=struct.unpack('>I',exact(conn,4))[0]
                if magic==0x52474c30:break
                assert magic==0x52474c31
                w,h,seq,flags,size,crc,raw_size=struct.unpack('>7I',exact(conn,28))
                assert seq==received+1 and 0<w<=4096 and 0<h<=8192 and raw_size==w*h*4 and size<=raw_size+1048576
                packed=exact(conn,size);raw=zlib.decompress(packed);total_packed+=size
                if flags==2:
                    assert len(raw)==raw_size;frame=bytearray(raw)
                else:
                    assert flags==3 and frame is not None
                    changes=struct.unpack_from('>I',raw)[0];at=4
                    for _ in range(changes):
                        offset,length=struct.unpack_from('>2I',raw,at);at+=8
                        assert offset+length<=len(frame) and at+length<=len(raw)
                        frame[offset:offset+length]=raw[at:at+length];at+=length
                    assert at==len(raw)
                assert zlib.crc32(frame)==crc, f'Frame {seq} CRC mismatch'
                now=time.monotonic()
                if not received:first_at=now;first_alpha=zlib.crc32(frame[3::4])
                last_at=now;received+=1
        proc.wait(timeout=20)
        status=(folder/'out'/(stem+'.log')).read_text(encoding='utf8',errors='replace')
        assert received==count and proc.returncode==0, f'received={received}/{count}, exit={proc.returncode}\n{status}'
        alpha=frame[3::4]
        report={'package':pkg,'frames':received,'resolution':[w,h],'allFrameRGBA_CRC':'PASS',
                'receiveDecodeVerifyFps':round((received-1)/(last_at-first_at),2),
                'includes':'capture, GPU readback, existing lossless codec, real ADB, host reconstruction and CRC verification; no GUI rendering',
                'compressedBytes':total_packed,'lastAlphaMin':min(alpha),'lastAlphaMax':max(alpha),
                'lastAlphaZero':alpha.count(0),'lastAlphaPartial':len(alpha)-alpha.count(0)-alpha.count(255),
                'lastAlphaOpaque':alpha.count(255),'firstAlphaCRC':first_alpha,'lastAlphaCRC':zlib.crc32(alpha)}
        (folder/'out'/(stem+'.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
        print(json.dumps(report,ensure_ascii=False),flush=True)
finally:
    if proc is not None and proc.poll() is None:proc.terminate()
    adb_run('-s',serial,'forward','--remove','tcp:'+str(port))
