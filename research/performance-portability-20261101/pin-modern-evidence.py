from pathlib import Path
import base64,hashlib,json,urllib.request
from concurrent.futures import ThreadPoolExecutor
out=Path(__file__).with_name('out')/'modern-sources'
sources=[('samsung-s24-android15-ScreenCapture.java','https://raw.githubusercontent.com/488315/samsung_framework/f090fe5941476ffeb9fa685b311de674f2620491/framework/sources/android/window/ScreenCapture.java',False),('aosp16qpr2-ScreenCaptureInternal.java','https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-qpr2-release/core/java/android/window/ScreenCaptureInternal.java?format=TEXT',True),('aosp16qpr2-ScreenCapture.java','https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-qpr2-release/core/java/android/window/ScreenCapture.java?format=TEXT',True),('lsposed-capture-api-references.java','https://raw.githubusercontent.com/LSPosed/DisableFlagSecure/main/app/src/main/java/io/github/lsposed/disableflagsecure/DisableFlagSecure.java',False)]
def fetch(item):
    name,url,encoded=item
    try:
        with urllib.request.urlopen(urllib.request.Request(url,headers={'User-Agent':'rc25-source-research'}),timeout=30) as response:data=response.read()
        if encoded:data=base64.b64decode(data)
        (out/name).write_bytes(data);text=data.decode('utf8',errors='replace')
        return dict(file=name,url=url,sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),hasNameFilter='ExcludeOrIncludeLayerNames' in text,hasHandleExclusion='setExcludeLayers' in text,screenCaptureInternal='ScreenCaptureInternal' in text)
    except Exception as error:return dict(file=name,url=url,error=str(error))
with ThreadPoolExecutor(max_workers=4) as pool:results=list(pool.map(fetch,sources))
(out/'pinned-index.json').write_text(json.dumps(results,indent=2),encoding='utf8')
for row in results:print(json.dumps(row))
