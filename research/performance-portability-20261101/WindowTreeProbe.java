package com.genymobile.scrcpy;
import android.os.Looper;
/** Metadata only. No capture, source selection, filtering or target View mutation. */
public final class WindowTreeProbe {
 public static void main(String[] args)throws Exception{
  if(android.os.Process.myUid()!=2000)throw new SecurityException();Looper.prepareMainLooper();Workarounds.apply();
  try(SceneContext context=new SceneContext(args[0],false)){System.out.println("WINDOW_TREE_JSON "+context.metadata().put("layerStabilityValidated",false).put("captureAttempted",false));}
  System.out.println("WINDOW_TREE_RESOURCES_RELEASED");System.exit(0);
 }
}
