"""Read public source artifacts only. No downloaded code is executed."""
from pathlib import Path
import hashlib,json,urllib.request
from concurrent.futures import ThreadPoolExecutor
folder=Path(__file__).resolve().parent;out=folder/'out/modern-sources';out.mkdir(exist_ok=True)
def get(url):
    request=urllib.request.Request(url,headers={'User-Agent':'rc25-source-research','Accept':'application/vnd.github+json'})
    with urllib.request.urlopen(request,timeout=30) as response:return response.read()
def repo(name):
    try:
        metadata=json.loads(get('https://api.github.com/repos/'+name));branch=metadata['default_branch']
        commit=json.loads(get('https://api.github.com/repos/'+name+'/commits/'+branch));revision=commit['sha']
        tree=json.loads(get('https://api.github.com/repos/'+name+'/git/trees/'+revision+'?recursive=1'))
        paths=[n['path'] for n in tree.get('tree',[]) if n['type']=='blob' and (n['path'].endswith('/ScreenCapture.java') or n['path'].endswith('/OplusScreenCapture.java') or n['path'].endswith('/SemSurfaceControl.java') or n['path'].endswith('/Build.java') or n['path'].endswith('/README.md'))]
        record=dict(repository=name,revision=revision,commitDate=commit['commit']['committer']['date'],truncated=tree.get('truncated'),candidatePaths=paths,files=[])
        for path in paths:
            if not any(path.endswith('/'+n) for n in ['ScreenCapture.java','OplusScreenCapture.java','SemSurfaceControl.java','Build.java']):continue
            url='https://raw.githubusercontent.com/'+name+'/'+revision+'/'+path;data=get(url)
            destination=out/(name.replace('/','-')+'-'+path.replace('/','__'));destination.write_bytes(data)
            text=data.decode('utf8',errors='replace')
            record['files'].append(dict(path=path,url=url,sha256=hashlib.sha256(data).hexdigest(),savedAs=destination.name,nameFilter='ExcludeOrIncludeLayerNames' in text,excludeHandles='setExcludeLayers' in text,captureModes=[s.strip() for s in text.splitlines() if 'setCaptureMode' in s or 'mCaptureMode' in s]))
        return record
    except Exception as error:return dict(repository=name,error=str(error))
with ThreadPoolExecutor(max_workers=2) as executor:records=list(executor.map(repo,['488315/samsung_framework','HBYShyw/AntiThermal']))
(out/'source-index.json').write_text(json.dumps(records,ensure_ascii=False,indent=2),encoding='utf8')
for record in records:print(json.dumps({k:v for k,v in record.items() if k!='candidatePaths'},ensure_ascii=False))
