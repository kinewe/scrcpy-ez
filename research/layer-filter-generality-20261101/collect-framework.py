"""Read public framework DEX metadata for the exact capture classes; no APK changes."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import base64, hashlib, json, struct, subprocess, sys, urllib.request, zipfile
from concurrent.futures import ThreadPoolExecutor

folder=Path(__file__).resolve().parent
out=folder/'out';out.mkdir(exist_ok=True)
adb=research_config.adb()
def metadata(data, prefixes=('Landroid/window/ScreenCapture', 'Landroid/view/SurfaceControl$CaptureArgs', 'Landroid/view/SurfaceControl$LayerCaptureArgs')):
    if not data.startswith(b'dex\n'):return []
    u32=lambda off:struct.unpack_from('<I',data,off)[0]
    u16=lambda off:struct.unpack_from('<H',data,off)[0]
    def uleb(off):
        value=0;shift=0
        while True:
            v=data[off];off+=1;value|=(v&127)<<shift
            if not v&128:return value,off
            shift+=7
    string_count,string_off=struct.unpack_from('<II',data,56)
    strings=[]
    for i in range(string_count):
        at=u32(string_off+i*4);_,at=uleb(at);strings.append(data[at:data.index(0,at)].decode('utf8',errors='replace'))
    types_count,types_off=struct.unpack_from('<II',data,64)
    types=[strings[u32(types_off+i*4)] for i in range(types_count)]
    proto_off=u32(76);field_off=u32(84);method_off=u32(92)
    def field(index):
        at=field_off+index*8
        return {'name':strings[u32(at+4)],'type':types[u16(at+2)]}
    def method(index):
        at=method_off+index*8;proto=proto_off+u16(at+2)*12;parameters=u32(proto+8)
        return {'name':strings[u32(at+4)],'returns':types[u32(proto+4)],
                'parameters':[types[u16(parameters+4+j*2)] for j in range(u32(parameters))] if parameters else []}
    result=[]
    count,offset=struct.unpack_from('<II',data,96)
    for i in range(count):
        at=offset+i*32;name=types[u32(at)]
        if not any(name.startswith(prefix) for prefix in prefixes):continue
        record={'class':name,'super':types[u32(at+8)] if u32(at+8)!=0xffffffff else None,'fields':[],'methods':[]}
        pos=u32(at+24)
        if pos:
            sizes=[]
            for _ in range(4):n,pos=uleb(pos);sizes.append(n)
            for size in sizes[:2]:
                index=0
                for _ in range(size):delta,pos=uleb(pos);flags,pos=uleb(pos);index+=delta;item=field(index);item['accessFlags']=flags;record['fields'].append(item)
            for size in sizes[2:]:
                index=0
                for _ in range(size):
                    delta,pos=uleb(pos);flags,pos=uleb(pos);code,pos=uleb(pos);index+=delta;item=method(index);item['accessFlags']=flags
                    if code:
                        insns=u32(code+12);item['codeSHA256']=hashlib.sha256(data[code+16:code+16+insns*2]).hexdigest()
                        if item['name'] in ['setCaptureMode','setExcludeOrIncludeLayerNames','getNativeExcludeOrIncludeLayerNames']:
                            item['dexCodeUnits']=[f'{x:04x}' for x in struct.unpack_from('<'+'H'*insns,data,code+16)]
                    record['methods'].append(item)
        result.append(record)
    return result

def device(label,serial):
    jar=out/(label+'-framework.jar')
    subprocess.run([adb,'-P',research_config.adb_port(),'-s',serial,'pull','/system/framework/framework.jar',str(jar)],check=True,timeout=90)
    classes=[]
    with zipfile.ZipFile(jar) as z:
        dex_names=[n for n in z.namelist() if n.endswith('.dex')]
        for name in dex_names:
            for item in metadata(z.read(name)):item['dex']=name;classes.append(item)
    fingerprint=subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',serial,'shell','getprop','ro.build.fingerprint'],timeout=15).decode().strip()
    report={'label':label,'fingerprint':fingerprint,'frameworkSHA256':hashlib.sha256(jar.read_bytes()).hexdigest(),'dexFiles':dex_names,'classes':classes}
    (out/(label+'-framework-metadata.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
    names=[m['name'] for c in classes for m in c['methods']]
    print(json.dumps({'device':label,'classes':len(classes),'nameFilterMethodPresent':'setExcludeOrIncludeLayerNames' in names,'fingerprint':fingerprint}),flush=True)

def aosp(ref):
    path='core/java/android/window/ScreenCapture.java'
    url=f'https://android.googlesource.com/platform/frameworks/base/+/{ref}/{path}?format=TEXT'
    try:
        with urllib.request.urlopen(url,timeout=25) as r:data=base64.b64decode(r.read())
        name='aosp-'+ref.replace('/','_')+'-ScreenCapture.java';(out/name).write_bytes(data)
        return {'ref':ref,'url':url,'sha256':hashlib.sha256(data).hexdigest(),'nameFilterParameterPresent':b'ExcludeOrIncludeLayerNames' in data,'file':name}
    except Exception as e:return {'ref':ref,'url':url,'error':str(e)}
if __name__ == '__main__':
    if sys.argv[1]=='aosp':
        with ThreadPoolExecutor(max_workers=3) as pool:records=list(pool.map(aosp,['android-16.0.0_r1','refs/heads/android16-security-release','refs/heads/android16-qpr2-release']))
        (out/'aosp-comparison.json').write_text(json.dumps(records,ensure_ascii=False,indent=2),encoding='utf8');print(json.dumps(records))
    else:device(sys.argv[1],sys.argv[2])
