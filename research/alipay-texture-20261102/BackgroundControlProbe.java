package com.genymobile.scrcpy;
import android.os.IBinder;
import java.lang.reflect.Method;
import org.json.JSONObject;

/** Opaque-background negative control only; never a transparent-stream solution. */
public final class BackgroundControlProbe {
    static void colour(IBinder token,String root,int colour)throws Exception{
        Method method=NativeAlphaProbe.class.getDeclaredMethod("set",IBinder.class,String.class,String.class,char.class,Object.class);method.setAccessible(true);method.invoke(null,token,root,"setBackgroundColor",'I',colour);
    }
    static JSONObject measure(SceneContext scene,Object source,NativeAlphaReadback reader)throws Exception{
        String root=scene.nodes.get(0).id;boolean changed=false;
        try{
            changed=true;colour(scene.window,root,0xffff00ff);Thread.sleep(300);
            return SourceFrameProbe.measure(scene,source,reader,"control").put("opaqueBackgroundControl",true);
        }finally{
            // The enclosing product Session owns restoration to the ORIGINAL
            // background resource. Restore its current transparent trial state first.
            if(changed)colour(scene.window,root,0);
            System.out.println("BACKGROUND_CONTROL_RETURNED_TO_TRIAL_TRANSPARENT");
        }
    }
}
