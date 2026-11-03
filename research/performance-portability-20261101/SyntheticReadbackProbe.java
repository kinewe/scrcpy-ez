package com.genymobile.scrcpy;
import android.graphics.*;
import android.os.*;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.zip.CRC32;
import org.json.*;

/** Owned deterministic GPU inputs; no target app, camera, or virtual display. */
public final class SyntheticReadbackProbe {
 public static void main(String[] args)throws Exception{
  Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
  if(android.os.Process.myUid()!=2000)throw new SecurityException();Looper.prepareMainLooper();Workarounds.apply();
  int width=Integer.parseInt(args[0]),height=3200,count=60;int[] pixels=new int[width*height];
  for(int y=0;y<height;y++)for(int x=0;x<width;x++){int a=(x+y*3)&255;pixels[y*width+x]=(a<<24)|(((x*7)&255)<<16)|(((y*11)&255)<<8)|((x+y)&255);}
  Bitmap software=Bitmap.createBitmap(width,height,Bitmap.Config.ARGB_8888);software.setPixels(pixels,0,width,0,0,width,height);byte[] expected=new byte[width*height*4];software.copyPixelsToBuffer(ByteBuffer.wrap(expected));
  Bitmap hardware=software.copy(Bitmap.Config.HARDWARE,false);software.recycle();pixels=null;
  long[] total=new long[count],copy=new long[count],render=new long[count];String backend="";CRC32 crc=new CRC32();long atRun=System.nanoTime();
  try(NativeAlphaReadback reader=new NativeAlphaReadback(true)){
   for(int i=-12;i<count;i++){long at=System.nanoTime();NativeAlphaReadback.Frame frame=reader.read(hardware);long elapsed=System.nanoTime()-at;
    if(!frame.alphaExact||!Arrays.equals(expected,frame.rgba))throw new AssertionError("owned RGBA mismatch at "+i);
    if(i>=0){total[i]=elapsed;copy[i]=frame.planeCopyNs;render[i]=frame.renderNs;}backend=frame.backend;
   }
   NativeAlphaReadback.Frame frame=reader.read(hardware);crc.update(frame.rgba);
  }finally{hardware.recycle();}
  System.out.println("SYNTHETIC_JSON "+new JSONObject().put("width",width).put("height",height).put("frames",count).put("backend",backend).put("allFramesByteExact",true).put("crc",crc.getValue()).put("read",PipelineProbe.distribution(total)).put("copyPlane",PipelineProbe.distribution(copy)).put("render",PipelineProbe.distribution(render)).put("elapsedSeconds",(System.nanoTime()-atRun)/1e9));System.out.println("SYNTHETIC_RESOURCES_RELEASED");System.exit(0);
 }
}
