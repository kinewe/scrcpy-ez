package com.genymobile.scrcpy;

import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.os.Looper;
import android.view.Surface;
import java.lang.reflect.*;
import java.util.*;

/** Tests a vendor capture filter only on owned, off-screen layers. */
public final class NameFilterLab {
    static final Class<?> SC;
    static { try { SC = Class.forName("android.view.SurfaceControl"); } catch(Exception e) { throw new RuntimeException(e); } }
    static Object call(Object o,String n,Class<?>[] t,Object...a)throws Exception { return o.getClass().getMethod(n,t).invoke(o,a); }
    static Object layer(String name,Object parent,boolean colour,boolean container)throws Exception {
        Object b=Class.forName("android.view.SurfaceControl$Builder").getConstructor().newInstance();
        call(b,"setName",new Class[]{String.class},name);
        if(parent!=null)call(b,"setParent",new Class[]{SC},parent);
        if(container)call(b,"setContainerLayer",new Class[]{});
        else if(colour)call(b,"setColorLayer",new Class[]{});
        else {call(b,"setBufferSize",new Class[]{int.class,int.class},256,128);call(b,"setFormat",new Class[]{int.class},PixelFormat.RGBA_8888);}
        call(b,"setOpaque",new Class[]{boolean.class},false);
        return call(b,"build",new Class[]{});
    }
    static int[] capture(Object root,int mode,String[] names,Object camera,boolean realExclude)throws Exception {
        Object b=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(SC).newInstance(root);
        call(b,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,256,128));
        call(b,"setChildrenOnly",new Class[]{boolean.class},false);
        if(mode>=0)call(b,"setCaptureMode",new Class[]{int.class},mode);
        if(names!=null)call(b,"setExcludeOrIncludeLayerNames",new Class[]{String[].class},(Object)names);
        if(realExclude){Object excluded=Array.newInstance(SC,1);Array.set(excluded,0,camera);call(b,"setExcludeLayers",new Class[]{excluded.getClass()},excluded);}
        Object args=call(b,"build",new Class[]{});
        Object result=Class.forName("android.window.ScreenCapture").getMethod("captureLayers",args.getClass()).invoke(null,args);
        if(result==null)throw new IllegalStateException("null capture");
        try {
            Bitmap hw=(Bitmap)call(result,"asBitmap",new Class[]{});
            try {Bitmap cpu=hw.copy(Bitmap.Config.ARGB_8888,false);try{int[] pixels=new int[256*128];cpu.getPixels(pixels,0,256,0,0,256,128);return pixels;}finally{cpu.recycle();}}
            finally{hw.recycle();}
        } finally {((HardwareBuffer)call(result,"getHardwareBuffer",new Class[]{})).close();}
    }
    static String stats(int[] pixels,int[] reference){
        int zero=0,mid=0,opaque=0,bad=0,min=255,max=0;
        for(int i=0;i<pixels.length;i++){int a=pixels[i]>>>24;min=Math.min(min,a);max=Math.max(max,a);if(a==0)zero++;else if(a==255)opaque++;else mid++;if(reference!=null&&pixels[i]!=reference[i])bad++;}
        return " min="+min+" max="+max+" zero="+zero+" mid="+mid+" opaque="+opaque+" rgbaMismatch="+bad;
    }
    public static void main(String[] argv)throws Exception{
        if(android.os.Process.myUid()!=2000)throw new IllegalStateException("shell only");
        Looper.prepareMainLooper();Workarounds.apply();
        Object root=null,camera=null,cameraContent=null,ui=null,tx=null;Surface surface=null;
        long deadline=System.nanoTime()+60000000000L;
        try{
            root=layer("EZ_GENERAL_ROOT",null,false,true);
            camera=layer("EZ_GENERAL_CAMERA_ONLY",root,false,true);
            cameraContent=layer("EZ_GENERAL_RAW_STREAM",camera,true,false);
            ui=layer("EZ_GENERAL_UI_ONLY",root,false,false);
            surface=Surface.class.getConstructor().newInstance();call(surface,"copyFrom",new Class[]{SC},ui);
            Canvas canvas=surface.lockCanvas(null);canvas.drawColor(Color.TRANSPARENT,PorterDuff.Mode.CLEAR);
            Paint paint=new Paint();for(int x=0;x<256;x++){paint.setColor(Color.argb(x,255,80,30));canvas.drawRect(x,0,x+1,128,paint);}surface.unlockCanvasAndPost(canvas);
            tx=Class.forName("android.view.SurfaceControl$Transaction").getConstructor().newInstance();
            call(tx,"setLayerStack",new Class[]{SC,int.class},root,2147483634);
            call(tx,"setLayer",new Class[]{SC,int.class},camera,0);call(tx,"setLayer",new Class[]{SC,int.class},ui,1);
            call(tx,"setColor",new Class[]{SC,float[].class},cameraContent,new float[]{0,1,0});
            call(tx,"setWindowCrop",new Class[]{SC,int.class,int.class},cameraContent,256,128);
            for(Object s:new Object[]{root,camera,cameraContent,ui})call(tx,"show",new Class[]{SC},s);
            call(tx,"apply",new Class[]{});Thread.sleep(150);
            int[] baseline=capture(root,-1,null,camera,false),reference=capture(root,-1,null,camera,true);
            System.out.println("BASELINE"+stats(baseline,null));System.out.println("HANDLE_REFERENCE"+stats(reference,null));
            int bad=0;for(int i=0;i<reference.length;i++)if((reference[i]>>>24)!=(i%256))bad++;
            if(bad!=0)throw new AssertionError("reference alpha ramp mismatch="+bad);
            String actual="EZ_GENERAL_CAMERA_ONLY#"+call(camera,"getLayerId",new Class[]{});
            String[][] patterns={{"EZ_GENERAL_CAMERA_ONLY"},{actual},{"EZ_GENERAL_CAMERA"},{"DOES_NOT_EXIST"}};
            for(int mode=0;mode<=3;mode++)for(String[] names:patterns){
                if(System.nanoTime()>deadline)throw new IllegalStateException("lab deadline");
                long at=System.nanoTime();int[] pixels=capture(root,mode,names,camera,false);
                System.out.println("NAME_FILTER mode="+mode+" names="+Arrays.toString(names)+stats(pixels,reference)+" elapsedMs="+(System.nanoTime()-at)/1000000);
            }
            String content="EZ_GENERAL_RAW_STREAM#"+call(cameraContent,"getLayerId",new Class[]{});
            int[] all=capture(root,1,new String[]{actual,content},camera,false);
            int[] onlyUi=capture(root,2,new String[]{"EZ_GENERAL_UI_ONLY#"+call(ui,"getLayerId",new Class[]{})},camera,false);
            System.out.println("EXCLUDE_COMPLETE_SUBTREE"+stats(all,reference));
            System.out.println("INCLUDE_UI_ONLY"+stats(onlyUi,reference));
            if(!Arrays.equals(all,reference)||!Arrays.equals(onlyUi,reference))throw new AssertionError("name filter changed reference RGBA");
        }finally{
            if(tx!=null)try{for(Object s:new Object[]{ui,cameraContent,camera,root})if(s!=null)call(tx,"remove",new Class[]{SC},s);call(tx,"apply",new Class[]{});}finally{call(tx,"close",new Class[]{});}
            if(surface!=null)surface.release();for(Object s:new Object[]{ui,cameraContent,camera,root})if(s!=null)call(s,"release",new Class[]{});
            System.out.println("OWNED_LAYERS_CLEANED");
        }
        System.exit(0);
    }
}
