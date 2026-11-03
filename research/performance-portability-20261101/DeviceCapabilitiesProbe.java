package com.genymobile.scrcpy;
import android.os.Build;
import java.lang.reflect.*;
import org.json.*;

/** Cross-ROM read-only API inventory. No capture or target app invocation. */
public final class DeviceCapabilitiesProbe {
 public static void main(String[] args)throws Exception{
  if(android.os.Process.myUid()!=2000)throw new IllegalStateException("shell only");
  JSONObject report=new JSONObject().put("sdk",Build.VERSION.SDK_INT).put("release",Build.VERSION.RELEASE).put("model",Build.MODEL).put("manufacturer",Build.MANUFACTURER).put("fingerprint",Build.FINGERPRINT).put("uid",android.os.Process.myUid()).put("classLoaderScope","bootclasspath plus research DEX/server; no OEM screenshot APK classloader");
  try{report.put("sdkIntFull",Build.VERSION.class.getField("SDK_INT_FULL").getInt(null));}catch(ReflectiveOperationException absent){}
  JSONObject classes=new JSONObject();boolean nameFilter=false;
  for(String name:new String[]{"android.window.ScreenCapture","android.window.ScreenCapture$CaptureArgs$Builder","android.window.ScreenCapture$LayerCaptureArgs$Builder","android.window.ScreenCaptureInternal","android.window.ScreenCaptureInternal$CaptureArgs$Builder","android.window.ScreenCaptureInternal$LayerCaptureArgs$Builder","android.window.ScreenCapture$ScreenCaptureParams$Builder","android.view.SurfaceControl","android.view.SurfaceControl$CaptureArgs$Builder","android.view.SurfaceControl$LayerCaptureArgs$Builder","android.window.WindowInfosListenerForTest","com.oplus.screenshot.OplusScreenCapture","com.oplus.screenshot.OplusScreenCapture$CaptureArgs$Builder","com.samsung.android.view.SemSurfaceControl"}){
   JSONObject entry=new JSONObject();JSONArray methods=new JSONArray();
   try{Class<?> c=Class.forName(name);entry.put("present",true);for(Method m:c.getDeclaredMethods())if(m.getName().matches("(?i).*(capture|screenshot|exclude|include|mirror|layer|handle).*")){methods.put(m.toString());if(m.getName().equals("setExcludeOrIncludeLayerNames"))nameFilter=true;}entry.put("methods",methods);}
   catch(Throwable e){entry.put("present",false).put("error",e.getClass().getSimpleName()+":"+e.getMessage());}
   classes.put(name,entry);
  }
  report.put("classes",classes).put("nameFilterMethodPresent",nameFilter);System.out.println(report.toString());
 }
}
