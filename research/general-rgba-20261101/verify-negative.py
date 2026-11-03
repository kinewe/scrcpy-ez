from pathlib import Path
import json, subprocess, sys
folder=Path(__file__).resolve().parent
p=subprocess.run([sys.executable,str(folder/'run-wire-benchmark.py'),'com.tencent.wework','2','negative-main'],capture_output=True,text=True,encoding='utf8',errors='replace',timeout=30)
assert p.returncode!=0 and 'received=0/2' in p.stdout+p.stderr and 'no independent SurfaceView' in p.stdout+p.stderr
result={'case':'WeCom normal main page with no camera SurfaceView','expectedRejection':'PASS','receivedFrames':0,'productPropertiesChanged':False}
(folder/'out/negative-main.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(result,ensure_ascii=False))
