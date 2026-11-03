"""Bounded DEX call-reference search, not a decompiler/reachability proof."""
from pathlib import Path
import ast,struct,zipfile,json,hashlib,sys
folder=Path(__file__).resolve().parent;out=folder/'out'
source=ast.parse((folder/'inspect-framework.py').read_text(encoding='utf8'))
scope={'struct':struct};exec(compile(ast.Module(body=[n for n in source.body if isinstance(n,ast.ClassDef) and n.name=='Dex'],type_ignores=[]),'Dex-reader','exec'),scope)
Dex=scope['Dex']
sinks={'Landroid/graphics/HardwareRenderer;->setPictureCaptureCallback','Landroid/graphics/HardwareRenderer;->createHardwareBitmap','Landroid/graphics/HardwareRenderer;->createHardwareBitmapWithFlag','Landroid/graphics/HardwareRenderer;->setContentRoot','Landroid/view/ViewDebug;->startRenderingCommandsCapture','Landroid/view/ViewDebug;->capture','Landroid/view/ViewDebug;->setLayoutParameter'}
def methods(dex):
 for i in range(dex.u32(96)):
  off=dex.u32(100)+32*i;owner=dex.types[dex.u32(off)];pos=dex.u32(off+24)
  if not pos:continue
  counts=[]
  for unused in range(4):value,pos=dex.uleb(pos);counts.append(value)
  for unused in range(counts[0]+counts[1]):unused,pos=dex.uleb(pos);unused,pos=dex.uleb(pos)
  for count in counts[2:]:
   index=0
   for unused in range(count):
    delta,pos=dex.uleb(pos);flags,pos=dex.uleb(pos);code,pos=dex.uleb(pos);index+=delta
    if code:yield owner,dex.methods[index],flags,code
results=[]
for file in sys.argv[1:]:
 archive=Path(file);result=dict(path=archive.name,sha256=hashlib.sha256(archive.read_bytes()).hexdigest(),methodsScanned=0,decodeFailures=0,sinkCallers=[],selectedMethods=[])
 with zipfile.ZipFile(archive) as z:
  for name in z.namelist():
   if not name.endswith('.dex'):continue
   dex=Dex(z.read(name))
   for owner,method,flags,code in methods(dex):
    try:refs=dex.refs(code)
    except Exception as error:
     result['decodeFailures']+=1;continue
    result['methodsScanned']+=1
    hits=sorted(set(refs['calls'])&sinks)
    if hits:result['sinkCallers'].append(dict(dex=name,method=method,flags=flags,sinks=hits,calls=refs['calls'],strings=refs['strings']))
    if method=='Landroid/view/SurfaceView;->updateSurface' or any(method.endswith('->'+word) for word in ['castModeChanged','projectionModeChanged','addCastProjectionCallback','removeCastProjectionCallback']) or ('ViewRootImpl' in owner and any(word in method for word in ['notifyCastMode','notifyProjectionMode','setMiWindowCornerRadii','handleCastModeChange','handleProjectionModeChange'])) or (owner=='Landroid/os/GraphicsEnvironment;' and any(word in method for word in ['setupGpuLayers','enableGpuDebugLayers','isDebuggable','getDebugLayerAppPaths','canInjectLayers','debugLayerEnabled'])):
     result['selectedMethods'].append(dict(dex=name,method=method,flags=flags,**refs))
 results.append(result)
(out/'render-entrypoint-references.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf8')
for result in results:
 print(json.dumps({k:result[k] for k in ['path','methodsScanned','decodeFailures']}))
 print('SINK_CALLERS',json.dumps([dict(method=x['method'],sinks=x['sinks']) for x in result['sinkCallers']]))
 print('SELECTED',json.dumps(result['selectedMethods'],ensure_ascii=False))
