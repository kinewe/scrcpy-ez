package com.genymobile.scrcpy;
import java.lang.reflect.*;
import org.json.*;
/** Read-only inventory of real Binder and rendering entry points. */
public final class BridgeApiInventory {
 public static void main(String[] args)throws Exception{
  Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
  if(android.os.Process.myUid()!=2000)throw new SecurityException();
  JSONArray result=new JSONArray();
  for(String name:new String[]{"android.app.IActivityManager","android.view.IWindow","android.graphics.HardwareRenderer","android.view.SurfaceControlViewHost","com.android.internal.view.ScrollCaptureInternal","android.view.inspector.WindowInspector"}){
   JSONArray methods=new JSONArray();
   for(Method m:Class.forName(name).getDeclaredMethods()){
    if(name.equals("android.app.IActivityManager")&&!m.getName().matches("registerReceiverWithFeature|unregisterReceiver|broadcastIntentWithFeature"))continue;
    if(name.equals("android.graphics.HardwareRenderer")&&!m.getName().matches(".*(?i:picture|debugging|property|systemproperties|root).*"))continue;
    methods.put(m.toString());
   }
   result.put(new JSONObject().put("class",name).put("methods",methods));
  }
  System.out.println("BRIDGE_API_JSON "+result);System.exit(0);
 }
}
