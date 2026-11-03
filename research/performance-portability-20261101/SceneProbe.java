package com.genymobile.scrcpy;
import android.os.*;
import org.json.*;

/** Inventory plus real capture matrix, including TextureView/Flutter negative cases. */
public final class SceneProbe {
 public static void main(String[] args)throws Exception{
  Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
  if(android.os.Process.myUid()!=2000||args.length!=1)throw new IllegalArgumentException();Looper.prepareMainLooper();Workarounds.apply();
  try(SceneContext scene=new SceneContext(args[0]);NativeAlphaReadback readback=new NativeAlphaReadback(true)){
   JSONObject report=scene.metadata();JSONArray cases=new JSONArray();cases.put(scene.measure("baseline",null,readback));
   for(NativeAlphaLayers.Layer candidate:scene.candidates())cases.put(scene.measure("exclude-"+candidate.id,scene.subtree(candidate),readback).put("candidateLayer",candidate.name));
   for(NativeAlphaLayers.Layer layer:scene.layers)if(layer.uid==scene.uid&&layer.name.startsWith(scene.pkg+"/"+scene.activity+"#"))cases.put(scene.measure("diagnostic-exclude-entire-ui-root",new String[]{layer.name},readback));
   report.put("captureCases",cases).put("independentSurfaceCandidates",scene.candidates().size()).put("viewMutations",0).put("activityConversions",0);
   System.out.println("SCENE_JSON "+report.toString());
  }finally{System.out.println("SCENE_RESOURCES_RELEASED");}System.exit(0);
 }
}
