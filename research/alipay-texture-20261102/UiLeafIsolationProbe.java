package com.genymobile.scrcpy;

import java.util.*;
import org.json.*;

/** Diagnostic only: remove each large UI leaf briefly, then restore its original alpha. */
public final class UiLeafIsolationProbe {
    static JSONObject measure(SceneContext scene,Object source,NativeAlphaReadback reader)throws Exception{
        List<NativeAlphaProbe.Node> nodes=SceneContext.tree(scene.window);JSONArray trials=new JSONArray();
        for(NativeAlphaProbe.Node n:nodes){
            if(n.id.contains("TextureView")||!"VISIBLE".equals(n.property("getVisibility()")))continue;
            if((long)n.number("layout:getWidth()")*n.number("layout:getHeight()")<(long)scene.width*scene.height/2)continue;
            boolean leaf=true;for(NativeAlphaProbe.Node child:nodes)if(child.parent==n&&"VISIBLE".equals(child.property("getVisibility()"))){leaf=false;break;}
            if(!leaf)continue;
            float alpha=Float.parseFloat(n.property("drawing:getAlpha()"));
            JSONObject trial=new JSONObject().put("view",n.id).put("originalAlpha",alpha);
            try{
                WindowAdaptationProbe.setter(scene.window,n.id,"setAlpha",0f);Thread.sleep(250);
                trial.put("hidden",CaptureSourceMatrix.measure(scene,source,true,"UI-leaf-hidden",reader,36));
            }finally{
                WindowAdaptationProbe.setter(scene.window,n.id,"setAlpha",alpha);
                System.out.println("UI_LEAF_ALPHA_RESTORED view="+n.id+" alpha="+alpha);
            }
            Thread.sleep(250);trial.put("restored",CaptureSourceMatrix.measure(scene,source,true,"UI-leaf-restored",reader,36));trials.put(trial);
            if(trials.length()>=3)break;
        }
        return new JSONObject().put("label","UI-leaf-isolation-diagnostic").put("trials",trials).put("candidateSelectionUsesPackageNames",false).put("fullUiSolution",false);
    }
}
