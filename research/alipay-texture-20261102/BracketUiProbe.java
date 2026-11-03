package com.genymobile.scrcpy;
import android.graphics.*;
import java.util.*;
import org.json.*;

/** Time-bracketed diagnostic only. PNG and native captures are NOT the same frame. */
public final class BracketUiProbe {
    static JSONObject measure(SceneContext scene,Object source,NativeAlphaReadback reader,NativeAlphaPolicy.Plan plan)throws Exception{
        NativeAlphaProbe.Node camera=null,candidate=null;
        for(NativeAlphaProbe.Node node:scene.nodes)if(node.id.equals(plan.camera.id))camera=node;
        if(camera==null)throw new IllegalStateException("camera generation changed");
        for(NativeAlphaProbe.Node branch=camera;branch.parent!=null;branch=branch.parent){
            boolean above=false;
            for(NativeAlphaProbe.Node node:scene.nodes)if(node.parent==branch.parent){
                if(node==branch){above=true;continue;}
                if(above&&GenericUiProbe.visible(node)&&(candidate==null||(long)node.number("layout:getWidth()")*node.number("layout:getHeight()")>(long)candidate.number("layout:getWidth()")*candidate.number("layout:getHeight()")))candidate=node;
            }
        }
        if(candidate==null)throw new IllegalStateException("no structural UI sibling");
        JSONArray pairs=new JSONArray();
        for(int i=0;i<3;i++){
            JSONObject before=CaptureSourceMatrix.measure(scene,source,true,"before",reader,1,false,plan);
            long begin=System.nanoTime();byte[] png=WindowProbe.command(scene.window,"CAPTURE",candidate.id,32*1024*1024,6000);
            Bitmap bitmap=BitmapFactory.decodeByteArray(png,0,png.length);if(bitmap==null)throw new IllegalStateException("invalid PNG");
            JSONObject ui;
            try{byte[] rgba=new byte[bitmap.getWidth()*bitmap.getHeight()*4];bitmap.copyPixelsToBuffer(java.nio.ByteBuffer.wrap(rgba));ui=SceneContext.alpha(rgba,bitmap.getWidth(),bitmap.getHeight()).put("startedNs",begin).put("finishedNs",System.nanoTime()).put("width",bitmap.getWidth()).put("height",bitmap.getHeight());}finally{bitmap.recycle();}
            JSONObject after=CaptureSourceMatrix.measure(scene,source,true,"after",reader,1,false,plan);
            pairs.put(new JSONObject().put("before",before).put("uiPngDiagnostic",ui).put("after",after));
        }
        return new JSONObject().put("name","time-bracketed-ui-diagnostic").put("candidate",candidate.id).put("sameFrame",false).put("completeUiValidated",false).put("pairs",pairs);
    }
}
