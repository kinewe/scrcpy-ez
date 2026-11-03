"""Two-background diagnostic with independent gray/red/repeat-black validation.

This reconstructs a candidate matte, not native alpha. It must not be used as
an accepted product stream or silently fill pixels that fail temporal checks.
"""
from pathlib import Path
import json,time
import numpy as np
from PIL import Image
folder=Path(__file__).resolve().parent;out=folder/'out'
index=json.loads((out/'composite-response-index.json').read_text(encoding='utf8'))
images={};variation={};meta={};start=time.monotonic()
for phase in index:
    name=phase['phase'];frames=phase['frames'];h,w=frames[0]['height'],frames[0]['width']
    assert all((f['height'],f['width'])==(h,w) for f in frames)
    stack=np.stack([np.fromfile(out/Path(f['path']).name,dtype=np.uint8).reshape(h,w,4) for f in frames])
    assert np.all(stack[:,:,:,3]==255),'expected opaque composite input'
    images[name]=np.median(stack[:,:,:,:3],axis=0).astype(np.float32)/255
    variation[name]=np.max(np.ptp(stack[:,:,:,:3].astype(np.int16),axis=0),axis=2)
    meta[name]=dict(distinctRgbaCrc=len({f['rgbaCrc'] for f in frames}),samples=len(frames),unstablePixelsOver8=int((variation[name]>8).sum()))
def linear(a):return np.where(a<=.04045,a/12.92,((a+.055)/1.055)**2.4)
def encoded(a):
    a=np.maximum(a,0);return np.where(a<=.0031308,a*12.92,1.055*a**(1/2.4)-.055)
def stats(error,mask):
    values=error[mask];return dict(pixels=int(mask.sum()),mean=float(np.mean(values)),p50=float(np.percentile(values,50)),p95=float(np.percentile(values,95)),max=float(np.max(values)),fractionWithin8=float(np.mean(values<=8))) if values.size else dict(pixels=0)
models={};allpixels=np.ones((h,w),bool)
for model in ['encoded-rgb','linear-srgb']:
    convert=linear if model=='linear-srgb' else lambda a:a
    back=encoded if model=='linear-srgb' else lambda a:a
    black=convert(images['black']);white=convert(images['white'])
    delta=white-black;transmission=np.median(delta,axis=2)
    # Black/white training only. Independent validation never updates alpha/color.
    training=(variation['black']<=8)&(variation['white']<=8)&(np.ptp(delta,axis=2)<8/255)&(transmission>=-2/255)&(transmission<=1+2/255)
    responsive=transmission>.05
    alpha=1-np.clip(transmission,0,1)
    nonblack_ui=np.max(images['black'],axis=2)>16/255
    result=dict(trainingConsistentPixels=int(training.sum()),responsivePixels=int(responsive.sum()),validations={})
    for phase,bg in [('gray',[.5,.5,.5]),('red',[.34,0,0]),('repeatblack',[0,0,0])]:
        prediction=back(black+transmission[:,:,None]*convert(np.array(bg,dtype=np.float32)))
        error=np.max(np.abs(prediction-images[phase]),axis=2)*255
        result['validations'][phase]=dict(all=stats(error,allpixels),responsive=stats(error,responsive),trainingConsistent=stats(error,training),trainingAndResponsive=stats(error,training&responsive),nonblackUiEvidence=stats(error,nonblack_ui),trainingAndNonblackUi=stats(error,training&nonblack_ui))
    models[model]=result
    if model=='encoded-rgb':
        # Diagnostic exports are explicitly unvalidated; dynamic regions remain visible.
        straight=np.divide(black,np.maximum(alpha[:,:,None],1/255),out=np.zeros_like(black),where=alpha[:,:,None]>0)
        rgba=np.concatenate([np.clip(straight,0,1),alpha[:,:,None]],axis=2)
        Image.fromarray(np.rint(rgba*255).astype(np.uint8)).save(out/'unvalidated-two-background-matte.png')
        y,x=np.indices((h,w));tile=((x//100+y//100)%2)[:,:,None]
        checker=np.where(tile,np.array([.25,.5,.25]),np.array([.55,.25,.55]))
        composite=black+(1-alpha[:,:,None])*checker
        Image.fromarray(np.rint(np.clip(composite,0,1)*255).astype(np.uint8)).save(out/'unvalidated-two-background-composite.png')
        repeat=np.max(np.abs(images['repeatblack']-images['black']),axis=2)*255
        Image.fromarray(np.rint(np.clip(repeat*4,0,255)).astype(np.uint8)).save(out/'two-background-temporal-error.png')
        result['repeatBlackChangedOver8']=int((repeat>8).sum())
result=dict(width=w,height=h,phaseStats=meta,models=models,offlineAnalysisSeconds=time.monotonic()-start,nativeAlpha=False,groundTruthAlphaAvailable=False,throughputMeasured=False)
(out/'composite-analysis.json').write_text(json.dumps(result,indent=2),encoding='utf8')
print(json.dumps(result))
