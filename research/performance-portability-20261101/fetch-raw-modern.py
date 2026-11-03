from pathlib import Path
import hashlib,json,urllib.error,urllib.request
from concurrent.futures import ThreadPoolExecutor
out=Path(__file__).with_name('out')/'modern-sources';out.mkdir(exist_ok=True)
items=[('samsung','488315/samsung_framework','framework/sources/android/window/ScreenCapture.java'),('samsung','488315/samsung_framework','framework/sources/android/os/Build.java'),('samsung','488315/samsung_framework','framework/sources/com/samsung/android/view/SemSurfaceControl.java'),('oplus','HBYShyw/AntiThermal','oplus-framework/sources/android/window/ScreenCaptureExtImpl.java'),('oplus','HBYShyw/AntiThermal','oplus-framework/sources/android/view/SurfaceControlExtImpl.java'),('oplus','HBYShyw/AntiThermal','oplus-framework/sources/com/oplus/screenshot/OplusScreenCapture.java'),('oplus','HBYShyw/AntiThermal','oplus-framework/sources/android/os/Build.java')]
def fetch(item):
    label,repo,path=item
    for branch in ['main','master']:
        url=f'https://raw.githubusercontent.com/{repo}/{branch}/{path}'
        try:
            with urllib.request.urlopen(urllib.request.Request(url,headers={'User-Agent':'rc25-source-research'}),timeout=20) as response:data=response.read()
            destination=out/(label+'-'+path.replace('/','__'));destination.write_bytes(data);text=data.decode('utf8',errors='replace')
            return dict(label=label,url=url,path=path,bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),nameFilter='ExcludeOrIncludeLayerNames' in text,excludeHandles='setExcludeLayers' in text,sdkLines=[l.strip() for l in text.splitlines() if 'SDK_INT =' in l or 'CODENAME =' in l or 'VERSION_CODENAME' in l],savedAs=destination.name)
        except urllib.error.HTTPError as e:
            if e.code!=404:return dict(label=label,path=path,error=str(e))
        except Exception as e:return dict(label=label,path=path,error=str(e))
    return dict(label=label,path=path,error='not found at candidate raw paths')
with ThreadPoolExecutor(max_workers=5) as pool:results=list(pool.map(fetch,items))
(out/'raw-index.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf8')
for r in results:print(json.dumps(r))
