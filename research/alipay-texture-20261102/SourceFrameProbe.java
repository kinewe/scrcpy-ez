package com.genymobile.scrcpy;
import android.graphics.*;
import android.hardware.HardwareBuffer;
import org.json.*;

/** Same-buffer diagnostics of the original window, not a ViewDebug redraw. */
public final class SourceFrameProbe {
    static JSONObject measure(SceneContext scene,Object source,NativeAlphaReadback reader)throws Exception{
        return measure(scene,source,reader,"source");
    }
    static JSONObject measure(SceneContext scene,Object source,NativeAlphaReadback reader,String label)throws Exception{
        if(!label.matches("[a-z]+"))throw new IllegalArgumentException();
        Object b=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(scene.sc).newInstance(source);
        SceneContext.call(b,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,scene.width,scene.height));
        SceneContext.call(b,"setChildrenOnly",new Class[]{boolean.class},false);
        SceneContext.call(b,"setUid",new Class[]{long.class},(long)scene.uid);
        Object args=SceneContext.call(b,"build",SceneContext.NONE);JSONArray frames=new JSONArray();
        for(int i=0;i<3;i++){
            long at=System.nanoTime();Object capture=scene.capture(args);if(capture==null)throw new IllegalStateException("null capture");
            try{Bitmap bitmap=(Bitmap)SceneContext.call(capture,"asBitmap",SceneContext.NONE);
                try{
                    NativeAlphaReadback.Frame gpu=reader.read(bitmap);Bitmap cpu=bitmap.copy(Bitmap.Config.ARGB_8888,false);byte[] rgba=new byte[gpu.rgba.length];
                    try{cpu.copyPixelsToBuffer(java.nio.ByteBuffer.wrap(rgba));}finally{cpu.recycle();}
                    int errors=0;for(int p=3;p<rgba.length;p+=4)if(rgba[p]!=gpu.rgba[p])errors++;
                    String path="/data/local/tmp/rc25-temporal-"+label+"-"+i+".png";
                    try(java.io.FileOutputStream stream=new java.io.FileOutputStream(path)){if(!bitmap.compress(Bitmap.CompressFormat.PNG,100,stream))throw new IllegalStateException("PNG encoding");}
                    frames.put(SceneContext.alpha(rgba,gpu.width,gpu.height).put("path",path).put("capturedAtNs",at).put("gpuAlphaErrors",errors).put("sameBuffer",true));
                }finally{bitmap.recycle();}
            }finally{((HardwareBuffer)SceneContext.call(capture,"getHardwareBuffer",SceneContext.NONE)).close();}
            Thread.sleep(200);
        }
        return new JSONObject().put("name","original-window-png-diagnostic").put("notThroughputMeasurement",true).put("frames",frames);
    }
}
