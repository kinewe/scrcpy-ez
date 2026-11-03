package com.genymobile.scrcpy;
import android.os.*;
import java.io.*;
import java.util.*;
import java.util.zip.CRC32;
import org.json.*;

/** Repeatable 384-row changes; compares real codec CPU and allocations without camera heat. */
public final class CodecBenchmarkProbe {
 public static void main(String[] args)throws Exception{
  if(android.os.Process.myUid()!=2000)throw new SecurityException();int w=1440,h=3200,count=180;byte[] rgba=new byte[w*h*4];Arrays.fill(rgba,(byte)76);long[] wall=new long[count],cpu=new long[count];long inputBytes=0,packedBytes=0;
  CRC32 stream=new CRC32();OutputStream sink=new OutputStream(){public void write(int b){stream.update(b);}public void write(byte[] b,int o,int n){stream.update(b,o,n);}};
  long allocatedBefore=0,gcBefore=0;
  try(NativeAlphaFrameCodec codec=new NativeAlphaFrameCodec(true)){
   for(int i=-12;i<count;i++){
    if(i==0){codec.reset();Map<String,String> s=Debug.getRuntimeStats();allocatedBefore=Long.parseLong(s.get("art.gc.bytes-allocated"));gcBefore=Long.parseLong(s.get("art.gc.gc-count"));}
    int start=(i+12)*13%200;
    for(int block=0;block<24;block++){int y0=((start+block)%200)*16;for(int y=y0;y<y0+16;y++)for(int x=0;x<w;x++){int p=(y*w+x)*4;int v=(x*7+y*11+(i+12)*23)&63;rgba[p]=(byte)v;rgba[p+1]=(byte)(v^31);rgba[p+2]=(byte)(v^15);rgba[p+3]=(byte)(76+((x+y)&127));}}
    long c=Debug.threadCpuTimeNanos(),at=System.nanoTime();codec.prepare(rgba,w,h,i==0||i%120==0);long elapsed=System.nanoTime()-at,used=Debug.threadCpuTimeNanos()-c;
    if(i>=0){wall[i]=elapsed;cpu[i]=used;inputBytes+=codec.inputBytes;packedBytes+=codec.packed.size();codec.packed.writeTo(sink);stream.update(codec.flags);}codec.commit();
   }
  }
  Map<String,String> end=Debug.getRuntimeStats();System.out.println("CODEC_JSON "+new JSONObject().put("frames",count).put("wall",PipelineProbe.distribution(wall)).put("threadCpu",PipelineProbe.distribution(cpu)).put("inputBytes",inputBytes).put("packedBytes",packedBytes).put("packedStreamCRC",stream.getValue()).put("allocatedBytesDelta",Long.parseLong(end.get("art.gc.bytes-allocated"))-allocatedBefore).put("gcCountDelta",Long.parseLong(end.get("art.gc.gc-count"))-gcBefore));System.exit(0);
 }
}
