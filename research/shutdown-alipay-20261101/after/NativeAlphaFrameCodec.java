package com.genymobile.scrcpy;

import java.io.*;
import java.util.zip.*;

/** V2: full premultiplied RGBA or lossless changed-row patches. CRC covers the full reconstructed frame. */
final class NativeAlphaFrameCodec implements AutoCloseable {
    final ByteArrayOutputStream packed=new ByteArrayOutputStream();
    private static final class PatchBuffer extends ByteArrayOutputStream {
        void feed(Deflater target){target.setInput(buf,0,count);}
    }
    private final PatchBuffer patch=new PatchBuffer();
    private final Deflater deflater=new Deflater(1);
    private final CRC32 crc=new CRC32();
    private final byte[] buffer=new byte[65536];
    private long[] previous=new long[0],current=new long[0];
    private boolean base;
    private final boolean profiling;
    long hashNs,patchNs,deflateNs;
    int inputBytes;
    NativeAlphaFrameCodec(){this(false);}
    NativeAlphaFrameCodec(boolean profiling){this.profiling=profiling;}
    private int width,height;
    int flags,changedRows;
    void reset(){base=false;}
    void prepare(byte[] rgba,int w,int h,boolean keyframe)throws IOException{
        long stageAt=profiling?System.nanoTime():0;
        int count=(h+15)/16,stride=w*4*16;
        if(w!=width||h!=height){width=w;height=h;base=false;previous=new long[count];current=new long[count];}
        int changed=0;
        for(int i=0;i<count;i++){int start=i*stride,length=Math.min(stride,rgba.length-start);crc.reset();crc.update(rgba,start,length);current[i]=crc.getValue();if(!base||current[i]!=previous[i])changed++;}
        if(profiling){hashNs=System.nanoTime()-stageAt;stageAt=System.nanoTime();}
        packed.reset();deflater.reset();changedRows=0;
        if(!base||keyframe||changed*4>=count*3){flags=2;deflater.setInput(rgba);changedRows=h;inputBytes=rgba.length;}
        else{
            flags=3;patch.reset();DataOutputStream data=new DataOutputStream(patch);data.writeInt(changed);
            for(int i=0;i<count;i++)if(current[i]!=previous[i]){int start=i*stride,length=Math.min(stride,rgba.length-start);data.writeInt(start);data.writeInt(length);data.write(rgba,start,length);changedRows+=length/(w*4);}
            inputBytes=patch.size();patch.feed(deflater);
        }
        if(profiling){patchNs=System.nanoTime()-stageAt;stageAt=System.nanoTime();}
        deflater.finish();while(!deflater.finished()){int n=deflater.deflate(buffer);packed.write(buffer,0,n);}
        if(profiling)deflateNs=System.nanoTime()-stageAt;
    }
    void commit(){long[] swap=previous;previous=current;current=swap;base=true;}
    public void close(){deflater.end();}
}
