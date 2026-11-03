package com.genymobile.scrcpy;
import java.util.*;
/** The optional telemetry must not change any emitted protocol bytes. */
public final class CodecProfilingParity {
 public static void main(String[] args)throws Exception{
  try(BaselineCodec old=new BaselineCodec();NativeAlphaFrameCodec normal=new NativeAlphaFrameCodec();NativeAlphaFrameCodec profile=new NativeAlphaFrameCodec(true)){
   Random random=new Random(2501);int w=64,h=80;byte[] frame=new byte[w*h*4];random.nextBytes(frame);
   for(int i=0;i<64;i++){
    if(i==15){old.reset();normal.reset();profile.reset();}
    if(i==20){w=96;h=48;frame=new byte[w*h*4];random.nextBytes(frame);}
    if(i==36){w=65;h=81;frame=new byte[w*h*4];random.nextBytes(frame);}
    if(i==50){w=1;h=1;frame=new byte[w*h*4];random.nextBytes(frame);}
    if(i%4==1)frame[(i*701)%frame.length]^=37;
    if(i%7==3)random.nextBytes(frame);
    boolean key=i%11==0;old.prepare(frame,w,h,key);normal.prepare(frame,w,h,key);profile.prepare(frame,w,h,key);
    if(old.flags!=normal.flags||old.flags!=profile.flags||old.changedRows!=profile.changedRows||!Arrays.equals(old.packed.toByteArray(),normal.packed.toByteArray())||!Arrays.equals(old.packed.toByteArray(),profile.packed.toByteArray()))throw new AssertionError("protocol mismatch frame="+i);
    old.commit();normal.commit();profile.commit();
   }
  }
  System.out.println("PASS: 64 no-copy codec cases byte-identical to baseline: full, delta, static, keyframe, reset, resize, partial row block, and 1x1");
 }
}
