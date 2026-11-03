package com.genymobile.scrcpy;

import android.graphics.*;
import android.hardware.HardwareBuffer;
import java.io.*;
import java.nio.ByteBuffer;
import org.json.*;

/** Audit every captured buffer independently; hints affect only our bitmap wrapper. */
public final class BitmapHintProbe {
    static JSONObject measure(SceneContext scene,Object source)throws Exception{
        Object builder=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(scene.sc).newInstance(source);
        SceneContext.call(builder,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,scene.width,scene.height));
        SceneContext.call(builder,"setChildrenOnly",new Class[]{boolean.class},false);
        SceneContext.call(builder,"setUid",new Class[]{long.class},(long)scene.uid);
        Object args=SceneContext.call(builder,"build",new Class[0]);
        JSONArray results=new JSONArray();boolean savedPositive=false,savedNegative=false;
        try(NativeAlphaReadback reader=new NativeAlphaReadback(true)){
            for(int i=0;i<30;i++){
                Object captured=scene.capture(args);if(captured==null)throw new IllegalStateException("null capture");
                try{
                    Bitmap bitmap=(Bitmap)SceneContext.call(captured,"asBitmap",new Class[0]);
                    try{
                        boolean hasAlpha=bitmap.hasAlpha(),premult=bitmap.isPremultiplied();
                        NativeAlphaReadback.Frame f=reader.read(bitmap);byte[] gpu=f.rgba.clone();
                        JSONObject row=new JSONObject().put("sample",i).put("hasAlpha",hasAlpha).put("premultiplied",premult).put("gpu",SceneContext.alpha(gpu,f.width,f.height)).put("backend",f.backend);
                        Bitmap cpu=bitmap.copy(Bitmap.Config.ARGB_8888,false);byte[] reference=new byte[gpu.length];
                        try{cpu.copyPixelsToBuffer(ByteBuffer.wrap(reference));}finally{cpu.recycle();}
                        int alphaErrors=0,maxRgbError=0;
                        for(int p=0;p<gpu.length;p++){int delta=Math.abs((gpu[p]&255)-(reference[p]&255));if(p%4==3){if(delta!=0)alphaErrors++;}else maxRgbError=Math.max(maxRgbError,delta);}
                        row.put("cpu",SceneContext.alpha(reference,f.width,f.height)).put("alphaErrors",alphaErrors).put("maxRgbError",maxRgbError);
                        boolean positive=row.getJSONObject("gpu").getInt("min")<255;
                        if((positive&&!savedPositive)||(!positive&&!savedNegative)){
                            String label=positive?"positive":"negative";
                            String path="/data/local/tmp/yinmo-texture-audit-"+label+".png";
                            try(FileOutputStream output=new FileOutputStream(path)){if(!bitmap.compress(Bitmap.CompressFormat.PNG,100,output))throw new IOException("PNG failed");}
                            JSONArray views=new JSONArray();for(NativeAlphaProbe.Node n:SceneContext.tree(scene.window))if(n.id.contains("DecorView")||n.id.contains("TextureView"))views.put(new JSONObject().put("id",n.id).put("alpha",n.property("drawing:getAlpha()")).put("visibility",n.property("getVisibility()")));
                            row.put("diagnosticPng",path).put("viewsAfterCapture",views);
                            if(positive)savedPositive=true;else savedNegative=true;
                        }
                        bitmap.setHasAlpha(true);reader.invalidate();NativeAlphaReadback.Frame forced=reader.read(bitmap);
                        row.put("forcedHasAlpha",SceneContext.alpha(forced.rgba,forced.width,forced.height));
                        results.put(row);
                    }finally{bitmap.recycle();}
                }finally{((HardwareBuffer)SceneContext.call(captured,"getHardwareBuffer",new Class[0])).close();}
            }
        }
        return new JSONObject().put("label","same-buffer-per-frame-audit").put("samples",results).put("mutatesTarget",false).put("note","CPU copy and metadata hint apply only to owned screenshot bitmap; PNG files are diagnostics, not stream performance");
    }
}
