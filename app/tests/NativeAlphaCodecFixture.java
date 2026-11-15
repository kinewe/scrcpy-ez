package com.genymobile.scrcpy;
import java.io.*;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.zip.CRC32;
/** Cross-language fixtures: every reconstructed RGBA byte is compared by the production C decoder. */
public final class NativeAlphaCodecFixture {
 public static void main(String[] args)throws Exception{
  Random random=new Random(23);CRC32 crc=new CRC32();int epoch=1,w=73,h=129;byte[] rgba=new byte[w*h*4];random.nextBytes(rgba);
  try(DataOutputStream out=new DataOutputStream(new FileOutputStream(args[0]));
      NativeAlphaFrameCodec packed=new NativeAlphaFrameCodec();NativeAlphaFrameCodec codec=new NativeAlphaFrameCodec()){
   out.writeInt(160);
   for(int frame=0;frame<160;frame++){
    if(frame==80){epoch++;codec.setXorEnabled(true);packed.setXorEnabled(true);}
    if(frame==40){w=101;h=53;rgba=new byte[w*h*4];random.nextBytes(rgba);}
    if(frame%13==0){epoch++;codec.reset();packed.reset();}
    if(frame%17==0)random.nextBytes(rgba);else if(frame%5!=0){int row=random.nextInt(h);for(int i=row*w*4;i<(row+1)*w*4;i++)rgba[i]=(byte)random.nextInt(256);}
    int rowBytes=w*4,stride=rowBytes+(frame%2==0?20:0);
    ByteBuffer plane=ByteBuffer.allocateDirect((h-1)*stride+rowBytes);
    for(int y=0;y<h;y++){plane.position(y*stride);plane.put(rgba,y*rowBytes,rowBytes);}
    crc.reset();NativeAlphaFrameCodec.updateRows(crc,plane,stride,rowBytes,0,h);
    long directCrc=crc.getValue();crc.reset();crc.update(rgba);
    boolean keyframe=frame%19==0;
    codec.prepare(new byte[rgba.length],plane,stride,w,h,keyframe);
    packed.prepare(rgba,w,h,keyframe);
    if(directCrc!=crc.getValue()||codec.flags!=packed.flags||codec.changedRows!=packed.changedRows
       ||!Arrays.equals(codec.packed.toByteArray(),packed.packed.toByteArray()))throw new AssertionError("strided frame "+frame);
    if(frame%23==10){
        // A prepared frame can be dropped by a camera epoch change before it is sent.
        codec.reset();packed.reset();epoch++;
        codec.prepare(new byte[rgba.length],plane,stride,w,h,false);
        packed.prepare(rgba,w,h,false);
        if(codec.flags!=2||packed.flags!=2)throw new AssertionError("dropped frame recovery");
    }
    packed.commit();
    out.writeInt(codec.flags);out.writeInt(epoch);out.writeInt(w);out.writeInt(h);out.writeInt(codec.packed.size());out.writeInt((int)crc.getValue());codec.packed.writeTo(out);out.write(rgba);codec.commit();
   }
  }
 }
}
