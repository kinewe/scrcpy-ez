package com.genymobile.scrcpy;
import android.os.*;
import java.util.concurrent.atomic.AtomicBoolean;

/** Finish only the verified child scanner in the explicitly owned experiment display. */
public final class SceneCleanupProbe {
    public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length!=2)throw new IllegalArgumentException();
        Looper.prepareMainLooper();Workarounds.apply();
        try(SceneContext scene=new SceneContext(args[0],false)){
            if(scene.display!=Integer.parseInt(args[1]))throw new SecurityException("different display");
            Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
            NativeAlphaScene scope=new NativeAlphaScene(atm,scene.window,scene.display,scene.uid,scene.pkg,scene.activity,new AtomicBoolean(),true);
            try{scope.finishOriginal();}finally{scope.close(false);}
            System.out.println("SCOPED_CHILD_FINISHED display="+scene.display+" activity="+scene.activity);
        }
        System.exit(0);
    }
}
