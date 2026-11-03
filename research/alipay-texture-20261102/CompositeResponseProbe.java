package com.genymobile.scrcpy;

import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.os.*;
import java.io.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.*;

/** Read-only raw window samples for controlled virtual-camera backgrounds.
 * No View alpha, background, task mode, or window format is changed.
 */
public final class CompositeResponseProbe {
    public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length!=4||!args[2].matches("[a-z]+"))throw new IllegalArgumentException("package ownedDisplay phase samples");
        int display=Integer.parseInt(args[1]),count=Integer.parseInt(args[3]);
        if(display<=0||count<1||count>4)throw new IllegalArgumentException();
        Looper.prepareMainLooper();Workarounds.apply();
        try(SceneContext scene=new SceneContext(args[0],false);NativeAlphaReadback reader=new NativeAlphaReadback(true)){
            if(scene.display!=display||!scene.activity.endsWith("MainCaptureActivity"))throw new SecurityException("not owned scanner");
            Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
            NativeAlphaScene source=new NativeAlphaScene(atm,scene.window,scene.display,scene.uid,scene.pkg,scene.activity,new AtomicBoolean(),false);
            try{
                source.rebind();Object builder=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(scene.sc).newInstance(source.source());
                SceneContext.call(builder,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,scene.width,scene.height));
                SceneContext.call(builder,"setChildrenOnly",new Class[]{boolean.class},false);
                SceneContext.call(builder,"setUid",new Class[]{long.class},(long)scene.uid);
                Object captureArgs=SceneContext.call(builder,"build",SceneContext.NONE);JSONArray frames=new JSONArray();
                for(int i=0;i<count;i++){
                    long at=System.nanoTime();Object captured=scene.capture(captureArgs);if(captured==null)throw new IOException("null capture");
                    try{Bitmap bitmap=(Bitmap)SceneContext.call(captured,"asBitmap",SceneContext.NONE);
                        try{
                            NativeAlphaReadback.Frame frame=reader.read(bitmap);
                            String path="/data/local/tmp/rc25-composite-"+args[2]+"-"+i+".rgba";
                            try(FileOutputStream stream=new FileOutputStream(path)){stream.write(frame.rgba);}
                            java.util.zip.CRC32 crc=new java.util.zip.CRC32();crc.update(frame.rgba);
                            frames.put(new JSONObject().put("path",path).put("width",frame.width).put("height",frame.height).put("capturedAtNs",at).put("rgbaCrc",crc.getValue()));
                        }finally{bitmap.recycle();}
                    }finally{((HardwareBuffer)SceneContext.call(captured,"getHardwareBuffer",SceneContext.NONE)).close();}
                    Thread.sleep(70);
                }
                System.out.println("COMPOSITE_RESPONSE_JSON "+new JSONObject().put("scene",scene.metadata()).put("phase",args[2]).put("frames",frames).put("targetViewMutations",0));
            }finally{source.close(false);}
        }finally{System.out.println("COMPOSITE_RESPONSE_RESOURCES_RELEASED");}
        System.exit(0);
    }
}
