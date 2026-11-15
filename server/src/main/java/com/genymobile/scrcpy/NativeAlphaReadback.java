package com.genymobile.scrcpy;

import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.media.Image;
import android.media.ImageReader;
import java.nio.ByteBuffer;

/** Shell-owned renderer; capability and colour validation select the fast readback route. */
final class NativeAlphaReadback implements AutoCloseable {
    static final class Frame {int width,height,maxRgbError;byte[] rgba;String backend,inputColour,fallback="";boolean alphaExact,rgbaExact;
        long setupNs,renderNs,acquireNs,planeCopyNs,validationNs,imageTimestamp;int syncResult;
        ByteBuffer plane;
        int rowStride;
        int alphaAt(int pixel) {
            return (plane == null ? rgba[pixel * 4 + 3]
                                  : plane.get((pixel / width) * rowStride + (pixel % width) * 4 + 3)) & 255;
        }
    }
    private final boolean profiling;
    private final boolean direct;
    NativeAlphaReadback(){this(false);}
    NativeAlphaReadback(boolean profiling){this(profiling,false);}
    NativeAlphaReadback(boolean profiling,boolean direct){
        this.profiling=profiling;this.direct=direct;
        readbackPaint.setBlendMode(BlendMode.SRC);
        readbackPaint.setFilterBitmap(false);
        readbackPaint.setDither(false);
    }
    private ImageReader reader;
    private Image liveImage;
    private HardwareRenderer renderer;
    private RenderNode node;
    private final Paint readbackPaint = new Paint();
    private final Frame frame=new Frame();
    private boolean verified;
    private int failures;
    private long retryAt;
    private String colourKey="";
    private int[] pixels;
    private byte[] reference;
    void invalidate(){verified=false;failures=0;retryAt=0;frame.alphaExact=false;frame.rgbaExact=false;frame.fallback="";}

    Frame read(Bitmap hardware)throws Exception {
        releasePlane();
        if(profiling){frame.setupNs=frame.renderNs=frame.acquireNs=frame.planeCopyNs=frame.validationNs=frame.imageTimestamp=0;frame.syncResult=0;}
        int w=hardware.getWidth(),h=hardware.getHeight();
        if(frame.rgba==null||frame.width!=w||frame.height!=h){close();invalidate();frame.width=w;frame.height=h;frame.rgba=new byte[Math.multiplyExact(Math.multiplyExact(w,h),4)];pixels=null;reference=null;}
        String colour=hardware.getColorSpace()==null?"unknown":hardware.getColorSpace().getName();
        if(!colour.equals(colourKey)){colourKey=colour;invalidate();}
        frame.inputColour=colour;boolean srgb=hardware.getColorSpace()!=null&&hardware.getColorSpace().isSrgb();
        if(hardware.isPremultiplied()&&hardware.getColorSpace()!=null&&failures<2&&System.nanoTime()>=retryAt)try {
            frame.alphaExact=verified;
            long stageAt=profiling?System.nanoTime():0;
            if(reader==null){
                reader=ImageReader.newInstance(w,h,PixelFormat.RGBA_8888,3,HardwareBuffer.USAGE_CPU_READ_OFTEN|HardwareBuffer.USAGE_GPU_COLOR_OUTPUT|HardwareBuffer.USAGE_GPU_SAMPLED_IMAGE);
                renderer=new HardwareRenderer();renderer.setOpaque(false);renderer.setSurface(reader.getSurface());
                node=new RenderNode("ez-shell-alpha-readback");node.setPosition(0,0,w,h);renderer.setContentRoot(node);
            }
            if(profiling){frame.setupNs=System.nanoTime()-stageAt;stageAt=System.nanoTime();}
            RecordingCanvas canvas=node.beginRecording();canvas.drawColor(Color.TRANSPARENT,PorterDuff.Mode.CLEAR);canvas.drawBitmap(hardware,0,0,readbackPaint);node.endRecording();
            int result=renderer.createRenderRequest().setWaitForPresent(true).syncAndDraw();
            if(profiling){frame.renderNs=System.nanoTime()-stageAt;frame.syncResult=result;stageAt=System.nanoTime();}
            Image image=null;long deadline=System.nanoTime()+200000000L;
            while(image==null&&System.nanoTime()<deadline){image=reader.acquireLatestImage();if(image==null)Thread.sleep(1);}
            if(image==null)throw new IllegalStateException("readback timeout");
            if(profiling){frame.acquireNs=System.nanoTime()-stageAt;frame.imageTimestamp=image.getTimestamp();stageAt=System.nanoTime();}
            try {
                Image.Plane plane=image.getPlanes()[0];
                int stride=plane.getRowStride(),rowBytes=w*4;
                ByteBuffer bytes=plane.getBuffer();
                if(plane.getPixelStride()!=4||stride<rowBytes||bytes.limit()<(h-1L)*stride+rowBytes)
                    throw new IllegalStateException("RGBA plane layout");
                // Keep initial colour validation on the packed path.
                if(direct&&verified&&bytes.isDirect()){
                    frame.plane=bytes;
                    frame.rowStride=stride;
                    liveImage=image;
                }else{
                    for(int y=0;y<h;y++){bytes.position(y*stride);bytes.get(frame.rgba,y*rowBytes,rowBytes);}
                }
            } finally {if(liveImage!=image)image.close();}
            if(profiling){frame.planeCopyNs=System.nanoTime()-stageAt;stageAt=System.nanoTime();}
            if(!verified){
                // Do not certify an empty/opaque initialization frame as proof of transparent-alpha fidelity.
                int min=255,max=0;for(int i=3;i<frame.rgba.length;i+=4*97){int a=frame.rgba[i]&255;min=Math.min(min,a);max=Math.max(max,a);}
                if(min!=max||(min>0&&min<255)){
                    Bitmap software=hardware.copy(Bitmap.Config.ARGB_8888,false);
                    try{
                        if(reference==null)reference=new byte[frame.rgba.length];
                        if(srgb)software.copyPixelsToBuffer(ByteBuffer.wrap(reference));else referencePixels(software,reference);
                    }finally{software.recycle();}
                    int alphaErrors=0,maxError=0;
                    for(int i=0;i<reference.length;i++){int error=Math.abs((reference[i]&255)-(frame.rgba[i]&255));if(i%4==3){if(error!=0)alphaErrors++;}else maxError=Math.max(maxError,error);}
                    if(alphaErrors!=0||maxError>(srgb?0:1))throw new IllegalStateException("colour validation alphaErrors="+alphaErrors+" maxRgbError="+maxError);
                    verified=true;frame.alphaExact=true;frame.rgbaExact=srgb&&maxError==0;frame.maxRgbError=maxError;
                    System.out.println("NATIVE_ALPHA_READBACK selected="+(srgb?"gpu-plane":"gpu-colour-srgb")+" inputColour="+colour+" alphaExact=true rgbaExact="+frame.rgbaExact+" maxRgbError="+maxError+" size="+w+"x"+h);
                }
            }
            if(profiling)frame.validationNs=System.nanoTime()-stageAt;
            frame.backend=srgb?"gpu-plane":"gpu-colour-srgb";return frame;
        }catch(Exception|LinkageError error){
            failures++;retryAt=System.nanoTime()+2000000000L;verified=false;close();frame.fallback=error.getClass().getSimpleName()+":"+error.getMessage();if(frame.fallback.length()>240)frame.fallback=frame.fallback.substring(0,240);
            System.out.println("NATIVE_ALPHA_READBACK fallback="+frame.fallback+" retry="+failures+"/2");
        }
        Bitmap software=hardware.copy(Bitmap.Config.ARGB_8888,false);
        try{
            if(srgb&&software.isPremultiplied()){software.copyPixelsToBuffer(ByteBuffer.wrap(frame.rgba));frame.backend="cpu-premult";frame.rgbaExact=true;}
            else{referencePixels(software,frame.rgba);frame.backend="cpu-srgb";frame.rgbaExact=false;}
            frame.alphaExact=true;frame.maxRgbError=0;return frame;
        }finally{software.recycle();}
    }
    private void referencePixels(Bitmap bitmap,byte[] out){
        int w=bitmap.getWidth(),h=bitmap.getHeight();if(pixels==null||pixels.length!=w*h)pixels=new int[w*h];bitmap.getPixels(pixels,0,w,0,0,w,h);
        // getPixels normalizes to sRGB. Only RGB is premultiplied; A8 is never filtered or quantized again.
        for(int i=0,j=0;i<pixels.length;i++){int p=pixels[i],a=p>>>24;out[j++]=(byte)((((p>>>16)&255)*a+127)/255);out[j++]=(byte)((((p>>>8)&255)*a+127)/255);out[j++]=(byte)(((p&255)*a+127)/255);out[j++]=(byte)a;}
    }
    void releasePlane(){
        frame.plane=null;
        if(liveImage!=null){liveImage.close();liveImage=null;}
    }
    public void close(){releasePlane();if(renderer!=null){renderer.destroy();renderer=null;}if(node!=null){node.discardDisplayList();node=null;}if(reader!=null){reader.close();reader=null;}}
}
