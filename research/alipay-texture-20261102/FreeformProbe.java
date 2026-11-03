package com.genymobile.scrcpy;

import android.graphics.Rect;
import android.os.*;
import java.util.*;

/** Temporarily change only the known experiment task's windowing mode, then restore it. */
public final class FreeformProbe {
    static final Class<?>[] NONE=new Class<?>[0];
    static void apply(Object organizer,Object token,int mode,Rect bounds)throws Exception{
        Class<?> type=Class.forName("android.window.WindowContainerTransaction"),tk=Class.forName("android.window.WindowContainerToken");
        Object change=type.getConstructor().newInstance();
        SceneContext.call(change,"setWindowingMode",new Class[]{tk,int.class},token,mode);
        SceneContext.call(change,"setBounds",new Class[]{tk,Rect.class},token,bounds);
        SceneContext.call(organizer,"applyTransaction",new Class[]{type},change);
    }
    public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length<3||args.length>5)throw new IllegalArgumentException("package display task [exactSamples] [audit]");
        Looper.prepareMainLooper();Workarounds.apply();
        int display=Integer.parseInt(args[1]),task=Integer.parseInt(args[2]);
        Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
        Object selected=null;
        for(Object info:(List<?>)SceneContext.call(atm,"getAllRootTaskInfos",NONE))if(info.getClass().getField("taskId").getInt(info)==task&&info.getClass().getField("displayId").getInt(info)==display)selected=info;
        if(selected==null)throw new SecurityException("experiment task not on experiment display");
        android.content.ComponentName top=(android.content.ComponentName)selected.getClass().getField("topActivity").get(selected);
        if(top==null||!args[0].equals(top.getPackageName()))throw new SecurityException("different task owner");
        Object config=selected.getClass().getField("configuration").get(selected),wc=config.getClass().getField("windowConfiguration").get(config);
        int originalMode=(Integer)SceneContext.call(wc,"getWindowingMode",NONE);
        Rect originalBounds=new Rect((Rect)SceneContext.call(wc,"getBounds",NONE));
        Object token=selected.getClass().getField("token").get(selected),organizer=SceneContext.call(atm,"getWindowOrganizerController",NONE);
        boolean changed=false;
        try{
            Rect trialBounds=new Rect(originalBounds);trialBounds.inset(8,8);
            changed=true;apply(organizer,token,5,trialBounds);Thread.sleep(1200);
            int observed=-1;Rect observedBounds=null;
            for(Object info:(List<?>)SceneContext.call(atm,"getAllRootTaskInfos",NONE))if(info.getClass().getField("taskId").getInt(info)==task&&info.getClass().getField("displayId").getInt(info)==display){Object cf=info.getClass().getField("configuration").get(info),window=cf.getClass().getField("windowConfiguration").get(cf);observed=(Integer)SceneContext.call(window,"getWindowingMode",NONE);observedBounds=new Rect((Rect)SceneContext.call(window,"getBounds",NONE));}
            System.out.println("FREEFORM_TRIAL task="+task+" display="+display+" originalMode="+originalMode+" observedMode="+observed+" observedBounds="+observedBounds);
            if(observed==5)WindowAdaptationProbe.runTrial(args.length>4?new String[]{args[0],args[1],args[3],args[4]}:new String[]{args[0],args[1],args.length>3?args[3]:"12"});
            else System.out.println("FREEFORM_NOT_APPLIED: no alpha conclusion from this request");
        }finally{
            if(changed)apply(organizer,token,originalMode,originalBounds);
            System.out.println("FREEFORM_RESTORED mode="+originalMode+" bounds="+originalBounds);
        }
        System.exit(0);
    }
}
