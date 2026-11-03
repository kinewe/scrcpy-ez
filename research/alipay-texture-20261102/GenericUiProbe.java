package com.genymobile.scrcpy;

import android.graphics.*;
import android.os.*;
import java.nio.ByteBuffer;
import java.util.*;
import org.json.*;

/** Structure-only UI candidate selection; PNG captures are diagnostic, never a streaming solution. */
public final class GenericUiProbe {
    static boolean visible(NativeAlphaProbe.Node n){return "VISIBLE".equals(n.property("getVisibility()"))&&n.number("layout:getWidth()")>0&&n.number("layout:getHeight()")>0;}
    public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length!=2)throw new IllegalArgumentException("package ownedDisplay");
        Looper.prepareMainLooper();Workarounds.apply();
        try(SceneContext scene=new SceneContext(args[0],false)){
            if(scene.display!=Integer.parseInt(args[1]))throw new SecurityException("experiment display changed");
            List<NativeAlphaProbe.Node> previews=new ArrayList<>();
            for(NativeAlphaProbe.Node n:scene.nodes)if(visible(n)&&n.id.contains("TextureView@")&&(long)n.number("layout:getWidth()")*n.number("layout:getHeight()")>(long)scene.width*scene.height/3)previews.add(n);
            if(previews.size()!=1)throw new IllegalStateException("ambiguous or missing large texture preview");
            NativeAlphaProbe.Node preview=previews.get(0);List<NativeAlphaProbe.Node> candidates=new ArrayList<>();
            JSONArray ancestors=new JSONArray();
            for(NativeAlphaProbe.Node branch=preview;branch.parent!=null;branch=branch.parent){
                ancestors.put(branch.parent.id);
                boolean above=false;
                for(NativeAlphaProbe.Node n:scene.nodes)if(n.parent==branch.parent){if(n==branch){above=true;continue;}if(above&&visible(n))candidates.add(n);}
            }
            if(candidates.isEmpty())throw new IllegalStateException("no independent UI siblings");
            JSONArray captures=new JSONArray();
            for(NativeAlphaProbe.Node n:candidates){
                long at=System.nanoTime();byte[] png=WindowProbe.command(scene.window,"CAPTURE",n.id,32*1024*1024,6000);
                Bitmap bitmap=BitmapFactory.decodeByteArray(png,0,png.length);if(bitmap==null)throw new IllegalStateException("empty diagnostic capture");
                try{byte[] rgba=new byte[bitmap.getWidth()*bitmap.getHeight()*4];bitmap.copyPixelsToBuffer(ByteBuffer.wrap(rgba));captures.put(SceneContext.alpha(rgba,bitmap.getWidth(),bitmap.getHeight()).put("view",n.id).put("width",bitmap.getWidth()).put("height",bitmap.getHeight()).put("pngBytes",png.length).put("diagnosticMs",(System.nanoTime()-at)/1e6));}finally{bitmap.recycle();}
            }
            Set<String> after=new HashSet<>();for(NativeAlphaProbe.Node n:SceneContext.tree(scene.window))if(visible(n))after.add(n.id);
            boolean stable=after.contains(preview.id);for(NativeAlphaProbe.Node n:candidates)stable&=after.contains(n.id);
            System.out.println("GENERIC_UI_JSON "+new JSONObject().put("package",scene.pkg).put("display",scene.display).put("activity",scene.activity).put("preview",preview.id).put("ancestorPath",ancestors).put("candidates",captures).put("bindingsStableAfter",stable).put("targetMutations",0).put("packageSpecificSelectors",false).put("customDrawingAndZOrderValidated",false).put("usesPngDiagnostic",true));
        }finally{System.out.println("GENERIC_UI_RESOURCES_RELEASED");}
        System.exit(0);
    }
}
