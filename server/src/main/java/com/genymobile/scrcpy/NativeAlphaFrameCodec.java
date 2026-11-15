package com.genymobile.scrcpy;

import java.io.*;
import java.nio.ByteBuffer;
import java.util.Arrays;
import java.util.zip.*;

/** Full premultiplied RGBA or lossless changed-row patches. CRC covers the reconstructed frame. */
final class NativeAlphaFrameCodec implements AutoCloseable {
    // Narrow strips avoid compressing unchanged rows around moving scan lines.
    private static final int ROWS_PER_BLOCK = 4;
    final ByteArrayOutputStream packed=new ByteArrayOutputStream();
    private static final class PatchBuffer extends ByteArrayOutputStream {
        int compressZstd(RgbaZstd encoder) { return encoder.compress(buf, count); }
        void feed(Deflater target){target.setInput(buf,0,count);}
        void xorRows(byte[] base, int offset, int length) {
            int start = count - length;
            for (int i = 0; i < length; ++i) {
                buf[start + i] ^= base[offset + i];
            }
        }
        void applyXor(byte[] base) {
            ByteBuffer rows = ByteBuffer.wrap(buf, 0, count);
            int blocks = rows.getInt();
            for (int i = 0; i < blocks; ++i) {
                int offset = rows.getInt();
                int length = rows.getInt();
                int start = rows.position();
                for (int j = 0; j < length; ++j) {
                    base[offset + j] ^= buf[start + j];
                }
                rows.position(start + length);
            }
        }
        void appendRows(ByteBuffer plane, int rowStride, int rowBytes, int firstRow, int rows) {
            int needed = count + rows * rowBytes;
            if (needed > buf.length) buf = Arrays.copyOf(buf, Math.max(needed, buf.length * 2));
            copyRows(plane, rowStride, rowBytes, firstRow, rows, buf, count);
            count = needed;
        }
    }
    private final PatchBuffer patch=new PatchBuffer();
    private final RgbaZstd zstd = new RgbaZstd();
    private final Deflater deflater=new Deflater(1);
    private final CRC32 crc=new CRC32();
    private final byte[] buffer=new byte[65536];
    private long[] previous=new long[0],current=new long[0];
    private boolean base;
    private boolean xorEnabled;
    private boolean zstdEnabled;
    private byte[] pixelBase;
    private byte[] fullFrame;
    private final boolean profiling;
    long hashNs,patchNs,deflateNs;
    int inputBytes;
    NativeAlphaFrameCodec(){this(false);}
    NativeAlphaFrameCodec(boolean profiling){this.profiling=profiling;}
    private int width,height;
    int flags,changedRows;
    void reset(){base=false;}
    void setXorEnabled(boolean enabled) {
        if (enabled != xorEnabled) {
            xorEnabled = enabled;
            reset();
        }
    }
    void setZstdEnabled(boolean enabled) { zstdEnabled = enabled; }
    void prepare(byte[] rgba,int w,int h,boolean keyframe)throws IOException{
        prepare(rgba, null, 0, w, h, keyframe);
    }
    static void updateRows(CRC32 crc, ByteBuffer plane, int rowStride, int rowBytes, int firstRow, int rows) {
        if (rowStride == rowBytes) {
            plane.limit((firstRow + rows) * rowBytes).position(firstRow * rowBytes);
            crc.update(plane);
            return;
        }
        for (int y = firstRow; y < firstRow + rows; y++) {
            plane.limit(y * rowStride + rowBytes).position(y * rowStride);
            crc.update(plane);
        }
    }
    private static void copyRows(ByteBuffer plane, int rowStride, int rowBytes, int firstRow,
                                 int rows, byte[] target, int offset) {
        if (rowStride == rowBytes) {
            plane.limit((firstRow + rows) * rowBytes).position(firstRow * rowBytes);
            plane.get(target, offset, rows * rowBytes);
            return;
        }
        for (int y = firstRow; y < firstRow + rows; y++) {
            plane.limit(y * rowStride + rowBytes).position(y * rowStride);
            plane.get(target, offset, rowBytes);
            offset += rowBytes;
        }
    }
    // rgba is scratch space for full frames when a live ImageReader plane is supplied.
    void prepare(byte[] rgba, ByteBuffer plane, int rowStride, int w, int h, boolean keyframe)throws IOException{
        long stageAt=profiling?System.nanoTime():0;
        int rowBytes=w*4,count=(h+ROWS_PER_BLOCK-1)/ROWS_PER_BLOCK,stride=rowBytes*ROWS_PER_BLOCK;
        if(w!=width||h!=height){width=w;height=h;base=false;previous=new long[count];current=new long[count];}
        int changed=0;
        for(int i=0;i<count;i++){
            int start=i*stride,length=Math.min(stride,rgba.length-start);
            crc.reset();
            if(plane==null) crc.update(rgba,start,length);
            else updateRows(crc,plane,rowStride,rowBytes,i*ROWS_PER_BLOCK,length/rowBytes);
            current[i]=crc.getValue();if(!base||current[i]!=previous[i])changed++;
        }
        if(profiling){hashNs=System.nanoTime()-stageAt;stageAt=System.nanoTime();}
        packed.reset();deflater.reset();changedRows=0;
        fullFrame=null;
        if(!base||keyframe||changed*4>=count*3){
            if(plane!=null) copyRows(plane,rowStride,rowBytes,0,h,rgba,0);
            if(xorEnabled) fullFrame=rgba;
            flags=2;deflater.setInput(rgba);changedRows=h;inputBytes=rgba.length;
        }
        else{
            flags=xorEnabled?4:3;patch.reset();DataOutputStream data=new DataOutputStream(patch);data.writeInt(changed);
            for(int i=0;i<count;i++)if(current[i]!=previous[i]){
                int start=i*stride,length=Math.min(stride,rgba.length-start),rows=length/rowBytes;
                data.writeInt(start);data.writeInt(length);
                if(plane==null) data.write(rgba,start,length);
                else patch.appendRows(plane,rowStride,rowBytes,i*ROWS_PER_BLOCK,rows);
                if(xorEnabled) patch.xorRows(pixelBase,start,length);
                changedRows+=rows;
            }
            inputBytes=patch.size();
            if (flags == 4 && zstdEnabled) {
                if (profiling) {
                    patchNs = System.nanoTime() - stageAt;
                    stageAt = System.nanoTime();
                }
                int length = patch.compressZstd(zstd);
                if (length > 0) {
                    packed.write(zstd.output, 0, length);
                    flags = 6;
                    if (profiling) deflateNs = System.nanoTime() - stageAt;
                    return;
                }
            }
            patch.feed(deflater);
        }
        if(profiling){patchNs=System.nanoTime()-stageAt;stageAt=System.nanoTime();}
        deflater.finish();while(!deflater.finished()){int n=deflater.deflate(buffer);packed.write(buffer,0,n);}
        if(profiling)deflateNs=System.nanoTime()-stageAt;
    }
    void commit(){
        if(fullFrame!=null){
            if(pixelBase==null||pixelBase.length!=fullFrame.length) pixelBase=new byte[fullFrame.length];
            System.arraycopy(fullFrame,0,pixelBase,0,fullFrame.length);
            fullFrame=null;
        }else if(flags==4||flags==6){
            patch.applyXor(pixelBase);
        }
        long[] swap=previous;previous=current;current=swap;base=true;
    }
    public void close() {
        try {
            zstd.close();
        } finally {
            deflater.end();
        }
    }
}
