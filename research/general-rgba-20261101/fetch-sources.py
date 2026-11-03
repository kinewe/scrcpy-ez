"""Fetch a small, pinned-branch set of primary AOSP sources for graphics experiments."""
from pathlib import Path
from urllib.request import urlopen
from concurrent.futures import ThreadPoolExecutor
import base64, json

folder = Path(__file__).resolve().parent / 'sources'
folder.mkdir(exist_ok=True)
items = [
 ('LayerCaptureArgs.aidl','native','libs/gui/aidl/android/gui/LayerCaptureArgs.aidl'),
 ('ISurfaceComposer.aidl','native','libs/gui/aidl/android/gui/ISurfaceComposer.aidl'),
 ('ISurfaceComposerClient.aidl','native','libs/gui/aidl/android/gui/ISurfaceComposerClient.aidl'),
 ('VirtualCameraRenderThread.cc','av','services/camera/virtualcamera/VirtualCameraRenderThread.cc'),
 ('VirtualCameraRenderThread.cpp','av','services/camera/virtualcamera/VirtualCameraRenderThread.cpp'),
 ('VirtualCameraTextureUtil.cc','av','services/camera/virtualcamera/util/VirtualCameraTextureUtil.cc'),
 ('virtualcamera-directory.json','av','services/camera/virtualcamera/'),
]
def fetch(item):
    name, repo, path = item
    mode = 'JSON' if name.endswith('.json') else 'TEXT'
    url = f'https://android.googlesource.com/platform/frameworks/{repo}/+/refs/heads/android16-release/{path}?format={mode}'
    try:
        with urlopen(url,timeout=20) as r: data = r.read()
        if mode == 'TEXT': data = base64.b64decode(data)
        (folder/name).write_bytes(data)
        return {'file':name,'url':url,'bytes':len(data)}
    except Exception as e: return {'file':name,'url':url,'error':str(e)}
with ThreadPoolExecutor(max_workers=4) as pool: result = list(pool.map(fetch,items))
(folder/'fetch-manifest.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(result,ensure_ascii=False))
