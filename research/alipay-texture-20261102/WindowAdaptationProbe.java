package com.genymobile.scrcpy;

import android.hardware.display.DisplayManager;
import android.os.*;
import java.lang.reflect.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.*;

/** Isolated reversible trials using the product's own background/camera recovery implementation. */
public final class WindowAdaptationProbe {
    static final Class<?>[] NONE = new Class<?>[0];
    static Object field(Object object, String name) throws Exception {Field f=object.getClass().getDeclaredField(name);f.setAccessible(true);return f.get(object);}
    static Object invoke(Object object, String name, Class<?>[] types, Object...args) throws Exception {Method m=object.getClass().getDeclaredMethod(name,types);m.setAccessible(true);try{return m.invoke(object,args);}catch(InvocationTargetException e){throw new Exception(name,e.getCause());}}
    static void setter(IBinder window, String view, String name, float value) throws Exception {
        Method m=NativeAlphaProbe.class.getDeclaredMethod("set",IBinder.class,String.class,String.class,char.class,Object.class);m.setAccessible(true);m.invoke(null,window,view,name,'F',value);
    }
    static JSONObject measure(Object session, NativeAlphaReadback readback, String label) throws Exception {
        JSONArray frames=new JSONArray();
        for(int i=0;i<12;i++){
            long at=System.nanoTime();
            NativeAlphaReadback.Frame f=(NativeAlphaReadback.Frame)invoke(session,"capture",new Class[]{NativeAlphaReadback.class},readback);
            frames.put(SceneContext.alpha(f.rgba,f.width,f.height).put("elapsedMs",(System.nanoTime()-at)/1e6).put("alphaExact",f.alphaExact));
        }
        return new JSONObject().put("label",label).put("frames",frames);
    }
    public static void main(String[] args) throws Exception {
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length<2||args.length>4)throw new IllegalArgumentException("package ownedDisplay [exactSamples] [audit]");
        Looper.prepareMainLooper();Workarounds.apply();
        runTrial(args);
        System.exit(0);
    }
    static void runTrial(String[] args)throws Exception{
        try(SceneContext scene=new SceneContext(args[0],false);NativeAlphaReadback readback=new NativeAlphaReadback(true)){
            if(scene.display!=Integer.parseInt(args[1]))throw new SecurityException("different experiment display");
            boolean texture=false;for(NativeAlphaProbe.Node n:scene.nodes)if(n.id.contains("TextureView@")&&n.number("layout:getWidth()")>scene.width/2&&"VISIBLE".equals(n.property("getVisibility()")))texture=true;
            if(!texture)throw new IllegalStateException("no visible large texture preview");
            SceneContext.put("sc",Class.forName("android.view.SurfaceControl"));
            SceneContext.put("tx",Class.forName("android.view.SurfaceControl$Transaction"));
            SceneContext.put("argsClass",Class.forName("android.window.ScreenCapture$LayerCaptureArgs"));
            SceneContext.put("wm",Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null));
            Constructor<DisplayManager> dm=DisplayManager.class.getDeclaredConstructor(android.content.Context.class);dm.setAccessible(true);
            SceneContext.put("displays",dm.newInstance(FakeContext.get()));
            SceneContext.put("policy",new NativeAlphaPolicy(FakeContext.get(),args[0],null));
            Field enabled=NativeAlphaProbe.class.getDeclaredField("enabled");enabled.setAccessible(true);((AtomicBoolean)enabled.get(null)).set(true);
            Method select=NativeAlphaProbe.class.getDeclaredMethod("select");select.setAccessible(true);Object target=select.invoke(null);
            Class<?> sessionClass=Class.forName("com.genymobile.scrcpy.NativeAlphaProbe$Session");Constructor<?> constructor=sessionClass.getDeclaredConstructors()[0];constructor.setAccessible(true);
            Object session=null;String root=scene.nodes.get(0).id;Float elevation=null;boolean modified=false;
            NativeAlphaScene borrowed=null;Object borrowedSource=null;Boolean originalOpaque=null;boolean flagChanged=false;
            JSONArray trials=new JSONArray();
            try{
                String observed=scene.nodes.get(0).property("drawing:getElevation()");
                System.out.println("ROOT_ELEVATION_PROPERTY "+observed);
                if(observed!=null)elevation=Float.parseFloat(observed);
                session=constructor.newInstance(target);
                ((AtomicBoolean)field(session,"running")).set(false);Thread monitor=(Thread)field(session,"monitor");if(monitor!=null){monitor.interrupt();monitor.join(700);}
                NativeAlphaPolicy.Plan plan=(NativeAlphaPolicy.Plan)field(session,"plan");
                System.out.println("ADAPTATION_PLAN "+new JSONObject().put("restore",plan.restore).put("steps",plan.steps).put("display",scene.display));
                Thread.sleep(350);trials.put(measure(session,readback,"product-adaptation"));
                Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
                borrowed=new NativeAlphaScene(atm,scene.window,scene.display,scene.uid,scene.pkg,scene.activity,new AtomicBoolean(),false);borrowed.rebind();borrowedSource=borrowed.source();
                trials.put(CaptureSourceMatrix.measure(scene,borrowedSource,true,"exact-window-after-adaptation",readback,args.length>2?Integer.parseInt(args[2]):12,args.length>3&&"positive-audit".equals(args[3]),plan));
                if(args.length>3&&"audit".equals(args[3]))trials.put(BitmapHintProbe.measure(scene,borrowedSource));
                if(args.length>3&&"paired-ui".equals(args[3]))trials.put(BracketUiProbe.measure(scene,borrowedSource,readback,plan));
                if(args.length>3&&"source-proof".equals(args[3]))trials.put(SourceFrameProbe.measure(scene,borrowedSource,readback));
                if(args.length>3&&"background-control".equals(args[3]))trials.put(BackgroundControlProbe.measure(scene,borrowedSource,readback));
                if(args.length>3&&"primitive-refresh".equals(args[3]))trials.put(PrimitiveRefreshProbe.measure(scene,borrowed,readback,plan));
                if(args.length>3&&"leaf-control".equals(args[3]))trials.put(UiLeafIsolationProbe.measure(scene,borrowedSource,readback));
                // Primitive controls rebind the scene and release the prior handle.
                borrowedSource=borrowed.source();
                int layerId=(Integer)SceneContext.call(borrowedSource,"getLayerId",NONE);
                java.lang.Process dump=new ProcessBuilder("dumpsys","SurfaceFlinger").start();String text=new String(dump.getInputStream().readAllBytes(),java.nio.charset.StandardCharsets.UTF_8);dump.waitFor();
                java.util.regex.Matcher state=java.util.regex.Pattern.compile("(?ms)^\\s*Layer \\["+layerId+"\\] "+java.util.regex.Pattern.quote(scene.pkg+"/"+scene.activity+"#"+layerId)+"\\s*$((?:(?!^\\s*Layer \\[).)*)").matcher(text);
                if(state.find()){
                    java.util.regex.Matcher opacity=java.util.regex.Pattern.compile("\\bisOpaque=(true|false)\\b").matcher(state.group(1));
                    if(opacity.find())originalOpaque=Boolean.parseBoolean(opacity.group(1));
                }
                System.out.println("EXACT_LAYER_ORIGINAL_OPAQUE layer="+layerId+" value="+originalOpaque);
                if(originalOpaque!=null){
                    Object tx=Class.forName("android.view.SurfaceControl$Transaction").getConstructor().newInstance();
                    try{flagChanged=true;SceneContext.call(tx,"setOpaque",new Class[]{scene.sc,boolean.class},borrowedSource,false);SceneContext.call(tx,"apply",NONE);}finally{SceneContext.call(tx,"close",NONE);}
                    Thread.sleep(200);trials.put(measure(session,readback,"exact-layer-opaque-false"));
                }
                if(elevation!=null){
                    modified=true;setter(scene.window,root,"setElevation",elevation+16f);
                    WindowProbe.command(scene.window,"REQUEST_LAYOUT",root,65536,1500);WindowProbe.command(scene.window,"INVALIDATE",root,65536,1500);
                    Thread.sleep(350);trials.put(measure(session,readback,"root-elevation-plus16"));
                }
            }finally{
                if(flagChanged&&borrowedSource!=null&&originalOpaque!=null){Object tx=Class.forName("android.view.SurfaceControl$Transaction").getConstructor().newInstance();try{SceneContext.call(tx,"setOpaque",new Class[]{scene.sc,boolean.class},borrowedSource,originalOpaque);SceneContext.call(tx,"apply",NONE);}finally{SceneContext.call(tx,"close",NONE);}System.out.println("EXACT_LAYER_OPAQUE_RESTORED="+originalOpaque);}
                if(borrowed!=null)borrowed.close(false);
                if(modified&&elevation!=null)setter(scene.window,root,"setElevation",elevation);
                if(session!=null)invoke(session,"close",new Class[]{boolean.class},false);
                ((AtomicBoolean)enabled.get(null)).set(false);
                System.out.println("ADAPTATION_RESTORE originalElevation="+elevation+" observedElevation="+SceneContext.tree(scene.window).get(0).property("drawing:getElevation()"));
            }
            System.out.println("WINDOW_ADAPTATION_JSON "+new JSONObject().put("scene",scene.metadata()).put("trials",trials));
        }finally{System.out.println("WINDOW_ADAPTATION_RESOURCES_RELEASED");}
    }
}
