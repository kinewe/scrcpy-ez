package com.genymobile.scrcpy;

import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.os.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.*;

/** Read-only comparison of UID-filtered mirrors and verified exact window layer handles. */
public final class CaptureSourceMatrix {
    static final Class<?>[] NONE=new Class<?>[0];
    static JSONObject measure(SceneContext scene,Object root,boolean uid,String name,NativeAlphaReadback reader)throws Exception{
        return measure(scene,root,uid,name,reader,12);
    }
    static JSONObject measure(SceneContext scene,Object root,boolean uid,String name,NativeAlphaReadback reader,int count)throws Exception{
        return measure(scene,root,uid,name,reader,count,false);
    }
    static JSONObject measure(SceneContext scene,Object root,boolean uid,String name,NativeAlphaReadback reader,int count,boolean auditPositive)throws Exception{
        return measure(scene,root,uid,name,reader,count,auditPositive,null);
    }
    static JSONObject measure(SceneContext scene,Object root,boolean uid,String name,NativeAlphaReadback reader,int count,boolean auditPositive,NativeAlphaPolicy.Plan plan)throws Exception{
        if(count<1||count>300)throw new IllegalArgumentException("sample count 1..300");
        Object builder=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(scene.sc).newInstance(root);
        SceneContext.call(builder,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,scene.width,scene.height));
        SceneContext.call(builder,"setChildrenOnly",new Class[]{boolean.class},false);
        if(uid)SceneContext.call(builder,"setUid",new Class[]{long.class},(long)scene.uid);
        Object args=SceneContext.call(builder,"build",NONE);JSONArray frames=new JSONArray();Set<Long> crcs=new HashSet<>();boolean savedPositive=false;
        for(int i=0;i<count;i++){
            long at=System.nanoTime();Object captured=scene.capture(args);if(captured==null)throw new IllegalStateException("null source");
            try{Bitmap bitmap=(Bitmap)SceneContext.call(captured,"asBitmap",NONE);try{
                NativeAlphaReadback.Frame f=reader.read(bitmap);long elapsed=System.nanoTime()-at;long nonblack=0;java.util.zip.CRC32 crc=new java.util.zip.CRC32();crc.update(f.rgba);crcs.add(crc.getValue());
                for(int p=0;p<f.rgba.length;p+=4)if(f.rgba[p]!=0||f.rgba[p+1]!=0||f.rgba[p+2]!=0)nonblack++;
                JSONObject row=SceneContext.alpha(f.rgba,f.width,f.height).put("sample",i).put("nonblackRgb",nonblack).put("rgbaCrc",crc.getValue()).put("captureStartedNs",at).put("readbackFinishedNs",at+elapsed).put("captureReadMs",elapsed/1e6).put("alphaExact",f.alphaExact).put("rgbaExact",f.rgbaExact).put("maxRgbError",f.maxRgbError);
                if(plan!=null){
                    int partial=0,nonzero=0,min=255,max=0;
                    for(int p=3;p<f.rgba.length;p+=4){int a=f.rgba[p]&255;if(a<255)partial++;if(a>0)nonzero++;min=Math.min(min,a);max=Math.max(max,a);}
                    boolean spatial=NativeAlphaPolicy.spatialAlpha(min,max,partial,nonzero,f.width*f.height);
                    double coverage=NativeAlphaPolicy.cameraAlphaCoverage(plan,f.rgba,f.width,f.height,false);
                    // Diagnostic replay of current pixel predicates only. This is NOT
                    // product-session acceptance: source geometry, consecutive frames,
                    // drift, UI completeness and temporal recovery remain separate.
                    row.put("spatialAlpha",spatial).put("cameraAlphaCoverage",coverage)
                       .put("pixelPredicatesPass",f.alphaExact&&spatial&&coverage>=0.6);
                }
                if(auditPositive&&row.getInt("min")<255){
                    Bitmap cpu=bitmap.copy(Bitmap.Config.ARGB_8888,false);byte[] reference=new byte[f.rgba.length];
                    try{cpu.copyPixelsToBuffer(java.nio.ByteBuffer.wrap(reference));}finally{cpu.recycle();}
                    int alphaErrors=0,maxRgbError=0;
                    for(int p=0;p<reference.length;p++){int delta=Math.abs((reference[p]&255)-(f.rgba[p]&255));if(p%4==3){if(delta!=0)alphaErrors++;}else maxRgbError=Math.max(maxRgbError,delta);}
                    row.put("auditAlphaErrors",alphaErrors).put("auditMaxRgbError",maxRgbError).put("auditCpu",SceneContext.alpha(reference,f.width,f.height)).put("bitmapHasAlpha",bitmap.hasAlpha());
                    if(!savedPositive){
                        String path="/data/local/tmp/rc25-refresh-audit-positive.png";
                        try(java.io.FileOutputStream output=new java.io.FileOutputStream(path)){if(!bitmap.compress(Bitmap.CompressFormat.PNG,100,output))throw new java.io.IOException("PNG failed");}
                        row.put("diagnosticPng",path);savedPositive=true;
                        JSONArray views=new JSONArray();for(NativeAlphaProbe.Node n:SceneContext.tree(scene.window))if(n.id.contains("DecorView")||n.id.contains("TextureView"))views.put(new JSONObject().put("id",n.id).put("alpha",n.property("drawing:getAlpha()")));
                        row.put("viewsAfterCapture",views);
                    }
                }
                frames.put(row);
            }finally{bitmap.recycle();}}finally{((HardwareBuffer)SceneContext.call(captured,"getHardwareBuffer",NONE)).close();}
        }
        return new JSONObject().put("name",name).put("distinctRgbaCrc",crcs.size()).put("frames",frames).put("sampleCount",count);
    }
    public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length!=2)throw new IllegalArgumentException("package ownedDisplay");
        Looper.prepareMainLooper();Workarounds.apply();
        try(SceneContext scene=new SceneContext(args[0],false);NativeAlphaReadback readback=new NativeAlphaReadback(true)){
            if(scene.display!=Integer.parseInt(args[1]))throw new SecurityException("display changed");
            Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
            NativeAlphaScene window=new NativeAlphaScene(atm,scene.window,scene.display,scene.uid,scene.pkg,scene.activity,new AtomicBoolean(),false);
            try{
                scene.args(null);window.rebind();
                JSONArray cases=new JSONArray();
                cases.put(measure(scene,scene.mirror,true,"mirror-uid",readback));
                cases.put(measure(scene,scene.mirror,false,"mirror-no-uid",readback));
                cases.put(measure(scene,window.source(),true,"exact-window-uid",readback));
                cases.put(measure(scene,window.source(),false,"exact-window-no-uid",readback));
                System.out.println("SOURCE_MATRIX_JSON "+new JSONObject().put("display",scene.display).put("activity",scene.activity).put("sourceLayer",SceneContext.call(window.source(),"getLayerId",NONE)).put("cases",cases).put("targetMutations",0));
            }finally{window.close(false);}
        }finally{System.out.println("SOURCE_MATRIX_RESOURCES_RELEASED");}
        System.exit(0);
    }
}
