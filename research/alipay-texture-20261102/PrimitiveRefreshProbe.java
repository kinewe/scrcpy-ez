package com.genymobile.scrcpy;
import android.os.IBinder;
import java.lang.reflect.Method;
import org.json.*;

/** Bounded reversible primitive-only controls, not product strategies. */
public final class PrimitiveRefreshProbe {
    static void resource(IBinder window,String root,int value)throws Exception{
        Method m=NativeAlphaProbe.class.getDeclaredMethod("set",IBinder.class,String.class,String.class,char.class,Object.class);m.setAccessible(true);m.invoke(null,window,root,"setBackgroundResource",'I',value);
    }
    static JSONObject trial(SceneContext scene,NativeAlphaScene source,NativeAlphaReadback reader,NativeAlphaPolicy.Plan plan,String name)throws Exception{
        Thread.sleep(250);source.rebind();Object root=source.source();
        JSONObject result=CaptureSourceMatrix.measure(scene,root,true,name,reader,24,true,plan);
        result.put("sourceLayer",SceneContext.call(root,"getLayerId",SceneContext.NONE));
        result.put("diagnostic",SourceFrameProbe.measure(scene,root,reader,name));return result;
    }
    static JSONObject measure(SceneContext scene,NativeAlphaScene source,NativeAlphaReadback reader,NativeAlphaPolicy.Plan plan)throws Exception{
        String root=scene.nodes.get(0).id;String value=scene.nodes.get(0).property("drawing:getAlpha()");
        if(value==null)throw new IllegalStateException("root alpha cannot be restored");float alpha=Float.parseFloat(value);
        JSONArray results=new JSONArray();boolean alphaChanged=false,resourceChanged=false;
        try{
            WindowProbe.command(scene.window,"INVALIDATE",root,65536,1500);
            results.put(trial(scene,source,reader,plan,"invalidate"));
            alphaChanged=true;WindowAdaptationProbe.setter(scene.window,root,"setAlpha",Math.min(alpha,0.999f));
            results.put(trial(scene,source,reader,plan,"alphalayer"));
            WindowAdaptationProbe.setter(scene.window,root,"setAlpha",alpha);alphaChanged=false;
            resourceChanged=true;resource(scene.window,root,android.R.color.black);Thread.sleep(120);resource(scene.window,root,android.R.color.transparent);
            WindowProbe.command(scene.window,"REQUEST_LAYOUT",root,65536,1500);WindowProbe.command(scene.window,"INVALIDATE",root,65536,1500);
            results.put(trial(scene,source,reader,plan,"renewal"));
        }finally{
            if(alphaChanged)WindowAdaptationProbe.setter(scene.window,root,"setAlpha",alpha);
            if(resourceChanged)resource(scene.window,root,android.R.color.transparent);
            System.out.println("PRIMITIVE_REFRESH_RESTORED trialRootAlpha="+alpha+"; enclosing session restores original backgrounds");
        }
        return new JSONObject().put("name","primitive-refresh-controls").put("cases",results);
    }
}
