package com.genymobile.scrcpy;

import android.graphics.Bitmap;
import android.hardware.HardwareBuffer;
import android.net.*;
import android.os.*;
import java.io.*;
import java.util.*;
import java.util.zip.CRC32;
import org.json.*;

/** Timed capture/read/encode/send stages, using unchanged source application pixels. */
public final class PipelineProbe {
 static final String[] STAGES={"capture","readback","renderPresent","acquireImage","copyPlane","validation","frameCRC","encode","rowHash","patchBuild","deflate","send","total","encoderThreadCpu","loopThreadCpu"};
 static JSONObject memory(int frame)throws Exception{
  Runtime r=Runtime.getRuntime();JSONObject info=new JSONObject().put("frame",frame).put("javaUsed",r.totalMemory()-r.freeMemory()).put("javaTotal",r.totalMemory()).put("nativeAllocated",Debug.getNativeHeapAllocatedSize()).put("pssKb",Debug.getPss());
  JSONObject gc=new JSONObject();for(Map.Entry<String,String> e:Debug.getRuntimeStats().entrySet())if(e.getKey().matches(".*(gc-count|gc-time|bytes-allocated|blocking-gc-count|blocking-gc-time)$"))gc.put(e.getKey(),e.getValue());
  info.put("gc",gc).put("sampleUptimeMs",SystemClock.uptimeMillis());try{android.os.PowerManager p=(android.os.PowerManager)FakeContext.get().getSystemService("power");info.put("thermalStatus",p.getCurrentThermalStatus()).put("interactive",p.isInteractive()).put("powerSave",p.isPowerSaveMode());}catch(Throwable unavailable){}
  return info;
 }
 static JSONObject distribution(long[] values)throws Exception{
  long[] sorted=values.clone();Arrays.sort(sorted);double sum=0;for(long v:values)sum+=v;
  return new JSONObject().put("meanMs",sum/values.length/1e6).put("p50Ms",sorted[sorted.length/2]/1e6).put("p95Ms",sorted[Math.min(sorted.length-1,(int)(sorted.length*.95))]/1e6).put("p99Ms",sorted[Math.min(sorted.length-1,(int)(sorted.length*.99))]/1e6);
 }
 public static void main(String[] args)throws Exception{
  Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
  if(android.os.Process.myUid()!=2000||args.length<3||args.length>4)throw new IllegalArgumentException();
  String pkg=args[0],mode=args[2];int count=Integer.parseInt(args[1]);if(count<10||count>12000||!Arrays.asList("capture","encode","wire").contains(mode))throw new IllegalArgumentException();
  LocalServerSocket server=null;LocalSocket socket=null;DataOutputStream wire=null;
  if(mode.equals("wire")){if(args.length!=4||!args[3].matches("rc25_scope_[a-z0-9]+"))throw new IllegalArgumentException();server=new LocalServerSocket(args[3]);System.out.println("WIRE_LISTENING pid="+android.os.Process.myPid());System.out.flush();socket=server.accept();wire=new DataOutputStream(socket.getOutputStream());}
  Looper.prepareMainLooper();Workarounds.apply();android.os.PowerManager.WakeLock screenWake=null;
  try(SceneContext scene=new SceneContext(pkg);NativeAlphaReadback readback=new NativeAlphaReadback(true);NativeAlphaFrameCodec codec=new NativeAlphaFrameCodec(true)){
   if("1".equals(System.getenv("RC25_RESEARCH_WAKE"))){android.os.PowerManager power=(android.os.PowerManager)FakeContext.get().getSystemService("power");screenWake=power.newWakeLock(android.os.PowerManager.SCREEN_BRIGHT_WAKE_LOCK|android.os.PowerManager.ACQUIRE_CAUSES_WAKEUP,"ez-rc25:research-screen");screenWake.acquire(120000);System.out.println("SCREEN_WAKE_LOCK acquired="+screenWake.isHeld());}
   List<NativeAlphaLayers.Layer> candidates=scene.candidates();if(candidates.size()!=1)throw new IOException("single independent preview required");
   Object captureArgs=scene.args(scene.subtree(candidates.get(0)));long[][] timings=new long[STAGES.length][count];JSONArray memories=new JSONArray();memories.put(memory(-20));
   CRC32 crc=new CRC32();long previousCrc=-1,previousTimestamp=-1,adjacentDuplicates=0,timestampDuplicates=0,encodedBytes=0,changedRows=0,keyframes=0;JSONObject lastAlpha=null;long runAt=0,runEnd=0;
   System.out.println("PIPELINE_CONTEXT "+scene.metadata().put("mode",mode).put("count",count));
   for(int i=-20;i<count;i++){
    if(i==0){codec.reset();memories.put(memory(0));runAt=System.nanoTime();}
    long cpuAt=Debug.threadCpuTimeNanos(),at=System.nanoTime();Object capture=scene.capture(captureArgs);long capturedAt=System.nanoTime();if(capture==null)throw new IOException("capture null");
    try{Bitmap hw=(Bitmap)SceneContext.call(capture,"asBitmap",SceneContext.NONE);try{
     NativeAlphaReadback.Frame f=readback.read(hw);long readAt=System.nanoTime();
     if(i<0)continue;
     int min=255,max=0;for(int p=3;p<f.rgba.length;p+=4*97){int a=f.rgba[p]&255;min=Math.min(min,a);max=Math.max(max,a);}if(min==255||max==0||!f.alphaExact)throw new IOException("transparent source validation lost");
     crc.reset();crc.update(f.rgba);long frameCrc=crc.getValue(),crcAt=System.nanoTime();if(previousCrc==frameCrc)adjacentDuplicates++;previousCrc=frameCrc;
     if(previousTimestamp==f.imageTimestamp)timestampDuplicates++;previousTimestamp=f.imageTimestamp;
     long encodeCpuAt=Debug.threadCpuTimeNanos();if(!mode.equals("capture")){codec.prepare(f.rgba,scene.width,scene.height,i==0||i%120==0);encodedBytes+=codec.packed.size();changedRows+=codec.changedRows;if(codec.flags==2)keyframes++;}
     long encodedAt=System.nanoTime(),encodedCpuAt=Debug.threadCpuTimeNanos();
     if(wire!=null){wire.writeInt(0x52474c31);wire.writeInt(scene.width);wire.writeInt(scene.height);wire.writeInt(i+1);wire.writeInt(codec.flags);wire.writeInt(codec.packed.size());wire.writeInt((int)frameCrc);wire.writeInt(f.rgba.length);codec.packed.writeTo(wire);wire.flush();}
     long sentAt=System.nanoTime(),sentCpuAt=Debug.threadCpuTimeNanos();if(!mode.equals("capture"))codec.commit();
     long[] row={capturedAt-at,readAt-capturedAt,f.renderNs,f.acquireNs,f.planeCopyNs,f.validationNs,crcAt-readAt,encodedAt-crcAt,mode.equals("capture")?0:codec.hashNs,mode.equals("capture")?0:codec.patchNs,mode.equals("capture")?0:codec.deflateNs,sentAt-encodedAt,sentAt-at,mode.equals("capture")?0:encodedCpuAt-encodeCpuAt,sentCpuAt-cpuAt};
     for(int j=0;j<row.length;j++)timings[j][i]=row[j];
     if(i==count-1)lastAlpha=SceneContext.alpha(f.rgba,scene.width,scene.height).put("alphaExact",f.alphaExact).put("maxRgbError",f.maxRgbError).put("backend",f.backend);
    }finally{hw.recycle();}}finally{((HardwareBuffer)SceneContext.call(capture,"getHardwareBuffer",SceneContext.NONE)).close();}
    if(i>=0&&(i+1)%120==0){memories.put(memory(i+1));System.out.println("PIPELINE_PROGRESS frame="+(i+1));System.out.flush();}
   }
   runEnd=System.nanoTime();memories.put(memory(count));JSONObject stages=new JSONObject();for(int j=0;j<STAGES.length;j++)stages.put(STAGES[j],distribution(timings[j]));
   JSONObject report=new JSONObject().put("mode",mode).put("frames",count).put("elapsedSeconds",(runEnd-runAt)/1e9).put("loopFps",count*1e9/(runEnd-runAt)).put("stages",stages).put("memory",memories).put("adjacentDuplicateFrames",adjacentDuplicates).put("duplicateImageTimestamps",timestampDuplicates).put("encodedBytes",encodedBytes).put("meanChangedRows",(double)changedRows/count).put("keyframes",keyframes).put("lastAlpha",lastAlpha).put("viewMutations",0).put("activityConversions",0);
   System.out.println("PROFILE_JSON "+report.toString());
  }finally{if(screenWake!=null&&screenWake.isHeld()){screenWake.release();System.out.println("SCREEN_WAKE_LOCK released=true");}if(wire!=null){wire.writeInt(0x52474c30);wire.flush();wire.close();}if(socket!=null)socket.close();if(server!=null)server.close();System.out.println("PIPELINE_RESOURCES_RELEASED");}
  System.exit(0);
 }
}
