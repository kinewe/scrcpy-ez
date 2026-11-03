package com.genymobile.scrcpy;
import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.media.*;
import android.net.*;
import android.os.*;
import java.io.*;
import java.lang.reflect.*;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.concurrent.atomic.*;
import org.json.*;

/** Public NDK HardwareBuffer handle IPC between owned shell peers only.
 * CPU/GPU readbacks and an ACK intentionally serialize this correctness test.
 */
public final class BufferBridgeProbe {
 static final int N=24;
 static native int send(HardwareBuffer buffer,int fd);
 static native HardwareBuffer receive(int fd);
 static native long id(HardwareBuffer buffer);
 static int fd(LocalSocket socket)throws Exception{Method m=FileDescriptor.class.getDeclaredMethod("getInt$");m.setAccessible(true);return (Integer)m.invoke(socket.getFileDescriptor());}
 static long crc(Image image,int width,int height){
  Image.Plane plane=image.getPlanes()[0];if(plane.getPixelStride()!=4)throw new IllegalStateException("RGBA pixel stride");
  ByteBuffer data=plane.getBuffer();java.util.zip.CRC32 crc=new java.util.zip.CRC32();
  for(int y=0;y<height;y++){ByteBuffer row=data.duplicate();row.position(y*plane.getRowStride());row.limit(row.position()+width*4);crc.update(row);}
  return crc.getValue();
 }
 static void producer(String name,int width,int height)throws Exception{
  try(LocalServerSocket listener=new LocalServerSocket(name);ImageReader reader=ImageReader.newInstance(width,height,PixelFormat.RGBA_8888,3,HardwareBuffer.USAGE_CPU_READ_OFTEN|HardwareBuffer.USAGE_GPU_SAMPLED_IMAGE)){
   System.out.println("BUFFER_BRIDGE_READY pid="+android.os.Process.myPid()+" socket="+name);System.out.flush();
   try(LocalSocket socket=listener.accept()){
    if(socket.getPeerCredentials().getUid()!=2000)throw new SecurityException("shell peer only");socket.setSoTimeout(5000);
    DataInputStream input=new DataInputStream(socket.getInputStream());DataOutputStream output=new DataOutputStream(socket.getOutputStream());
    output.writeInt(width);output.writeInt(height);output.writeInt(N);output.writeInt(android.os.Process.myPid());output.flush();
    int consumerPid=input.readInt(),socketFd=fd(socket);HardwareRenderer renderer=new HardwareRenderer();RenderNode node=new RenderNode("rc25-owned-ui-before-composition");node.setPosition(0,0,width,height);
    Class<?> callbackType=Class.forName("android.graphics.HardwareRenderer$PictureCapturedCallback");AtomicInteger pictures=new AtomicInteger();AtomicReference<Picture> firstPicture=new AtomicReference<>(),lastPicture=new AtomicReference<>();
    Object callback=java.lang.reflect.Proxy.newProxyInstance(callbackType.getClassLoader(),new Class[]{callbackType},(o,m,a)->{if(m.getName().equals("onPictureCaptured")){pictures.incrementAndGet();firstPicture.compareAndSet(null,(Picture)a[0]);lastPicture.set((Picture)a[0]);}else if(m.getName().equals("hashCode"))return System.identityHashCode(o);else if(m.getName().equals("equals"))return o==a[0];return null;});
    try{
     HardwareRenderer.class.getMethod("setDebuggingEnabled",boolean.class).invoke(null,false);
     renderer.setOpaque(false);renderer.setSurface(reader.getSurface());renderer.setContentRoot(node);
     HardwareRenderer.class.getMethod("setPictureCaptureCallback",callbackType).invoke(renderer,callback);
     Paint paint=new Paint();paint.setAntiAlias(false);long start=System.nanoTime();int acknowledged=0;long lastExpectedCrc=0;
     for(int seq=1;seq<=N;seq++){
      RecordingCanvas c=node.beginRecording();paint.setColor(Color.WHITE);c.drawRect(width/4,0,width/2,height,paint);
      paint.setColor(Color.argb(128,0,255,0));c.drawRect(width/2,0,width*3/4,height,paint);
      paint.setColor(Color.argb(128,255,0,0));c.drawRect(width*3/4,seq*8,width,seq*8+64,paint);
      paint.setColor(Color.rgb(0,seq,255));c.drawRect(0,0,32,32,paint);node.endRecording();
      renderer.createRenderRequest().setWaitForPresent(true).syncAndDraw();
      Image image=null;long deadline=System.nanoTime()+1000000000L;
      while(image==null&&System.nanoTime()<deadline){image=reader.acquireNextImage();if(image==null)Thread.sleep(1);}
      if(image==null)throw new IOException("producer image timeout");
      try{long expected=crc(image,width,height);lastExpectedCrc=expected; // also waits for source GPU completion before IPC
       try(HardwareBuffer buffer=image.getHardwareBuffer()){
        output.writeInt(seq);output.writeLong(id(buffer));output.writeLong(expected);output.flush();
        int result=send(buffer,socketFd);if(result!=0)throw new IOException("sendHandle "+result);
        if(input.readInt()!=seq)throw new IOException("wrong ACK");acknowledged++;
       }
      }finally{image.close();} // buffer may be reused only after the consumer ACK
     }
     JSONObject replay=new JSONObject();Picture picture=lastPicture.get();
     ByteArrayOutputStream serialized=new ByteArrayOutputStream();
     if(picture!=null)Picture.class.getMethod("writeToStream",OutputStream.class).invoke(picture,serialized);
     byte[] pictureBytes=serialized.toByteArray();output.writeInt(pictureBytes.length);output.writeLong(lastExpectedCrc);output.write(pictureBytes);output.flush();
     replay.put("serializedBytes",pictureBytes.length);
     if(picture!=null){
      try(FileOutputStream stream=new FileOutputStream("/data/local/tmp/rc25-renderbridge-buffer-owned.skp")){Picture.class.getMethod("writeToStream",OutputStream.class).invoke(picture,stream);}
      replay.put("pictureWidth",picture.getWidth()).put("pictureHeight",picture.getHeight());
      Bitmap unscaled=Bitmap.createBitmap(width,height,Bitmap.Config.ARGB_8888);
      try{new Canvas(unscaled).drawPicture(picture);replay.put("manualCanvasNoScale",new JSONObject().put("emptyAlpha",Color.alpha(unscaled.getPixel(width/8,height/2))).put("opaqueAlpha",Color.alpha(unscaled.getPixel(width*3/8,height/2))).put("partialAlpha",Color.alpha(unscaled.getPixel(width*5/8,height/2))));}finally{unscaled.recycle();}
      try(FileInputStream stream=new FileInputStream("/data/local/tmp/rc25-renderbridge-buffer-owned.skp")){
       Picture decoded=(Picture)Picture.class.getMethod("createFromStream",InputStream.class).invoke(null,stream);
       if(decoded!=null){Bitmap unscaledDecoded=Bitmap.createBitmap(width,height,Bitmap.Config.ARGB_8888);try{new Canvas(unscaledDecoded).drawPicture(decoded);replay.put("serializedManualCanvas",new JSONObject().put("width",decoded.getWidth()).put("height",decoded.getHeight()).put("emptyAlpha",Color.alpha(unscaledDecoded.getPixel(width/8,height/2))).put("opaqueAlpha",Color.alpha(unscaledDecoded.getPixel(width*3/8,height/2))).put("partialAlpha",Color.alpha(unscaledDecoded.getPixel(width*5/8,height/2))));}finally{unscaledDecoded.recycle();}}
      }
      for(Bitmap.Config config:new Bitmap.Config[]{Bitmap.Config.ARGB_8888,Bitmap.Config.HARDWARE}){
       Bitmap bitmap=Bitmap.createBitmap(picture,width,height,config),cpu=bitmap.copy(Bitmap.Config.ARGB_8888,false);
       try{replay.put(config.toString(),new JSONObject().put("emptyAlpha",Color.alpha(cpu.getPixel(width/8,height/2))).put("opaqueAlpha",Color.alpha(cpu.getPixel(width*3/8,height/2))).put("partialAlpha",Color.alpha(cpu.getPixel(width*5/8,height/2))));}finally{cpu.recycle();bitmap.recycle();}
      }
     }
     System.out.println("BUFFER_BRIDGE_PRODUCER_JSON "+new JSONObject().put("pid",android.os.Process.myPid()).put("consumerPid",consumerPid).put("submitted",N).put("acknowledged",acknowledged).put("wallMs",(System.nanoTime()-start)/1e6).put("pictureCallbacks",pictures.get()).put("pictureReplay",replay).put("rendererDebuggingEnabled",false).put("targetAppAccessed",false));
    }finally{HardwareRenderer.class.getMethod("setPictureCaptureCallback",callbackType).invoke(renderer,new Object[]{null});renderer.destroy();System.out.println("BUFFER_BRIDGE_PRODUCER_RELEASED");}
   }
  }
 }
 static void consumer(String name)throws Exception{
  try(LocalSocket socket=new LocalSocket();NativeAlphaReadback readback=new NativeAlphaReadback(true)){
   socket.connect(new LocalSocketAddress(name));socket.setSoTimeout(5000);if(socket.getPeerCredentials().getUid()!=2000)throw new SecurityException("shell peer only");
   DataInputStream input=new DataInputStream(socket.getInputStream());DataOutputStream output=new DataOutputStream(socket.getOutputStream());
   int width=input.readInt(),height=input.readInt(),count=input.readInt(),producerPid=input.readInt();if(width<1||height<1||(long)width*height>5000000||count!=N)throw new SecurityException("unexpected source");
   output.writeInt(android.os.Process.myPid());output.flush();int socketFd=fd(socket),crcErrors=0,idErrors=0,sequenceErrors=0,alphaErrors=0;Set<Long> crcs=new HashSet<>(),ids=new HashSet<>();long start=System.nanoTime();
   for(int i=1;i<=count;i++){
    int sequence=input.readInt();long expectedId=input.readLong(),expectedCrc=input.readLong();
    try(HardwareBuffer buffer=receive(socketFd)){
     if(buffer==null)throw new IOException("recvHandle failed");long actualId=id(buffer);ids.add(actualId);if(actualId!=expectedId||actualId<0)idErrors++;
     Bitmap bitmap=Bitmap.wrapHardwareBuffer(buffer,ColorSpace.get(ColorSpace.Named.SRGB));if(bitmap==null)throw new IOException("invalid imported bitmap");
     try{NativeAlphaReadback.Frame frame=readback.read(bitmap);java.util.zip.CRC32 crc=new java.util.zip.CRC32();crc.update(frame.rgba);crcs.add(crc.getValue());if(crc.getValue()!=expectedCrc)crcErrors++;
      if(sequence!=i||(frame.rgba[(16*width+16)*4+1]&255)!=sequence)sequenceErrors++;
      for(int y=height/8;y<height;y+=height/8){if((frame.rgba[(y*width+width/8)*4+3]&255)!=0)alphaErrors++;if((frame.rgba[(y*width+width*3/8)*4+3]&255)!=255)alphaErrors++;if((frame.rgba[(y*width+width*5/8)*4+3]&255)!=128)alphaErrors++;}
     }finally{bitmap.recycle();}
    }
    output.writeInt(sequence);output.flush();
   }
   int pictureBytes=input.readInt();long expectedPictureCrc=input.readLong();if(pictureBytes<1||pictureBytes>1024*1024)throw new IOException("invalid owned SKP length");
   byte[] bytes=new byte[pictureBytes];input.readFully(bytes);Picture decoded=(Picture)Picture.class.getMethod("createFromStream",InputStream.class).invoke(null,new ByteArrayInputStream(bytes));
   Bitmap replay=Bitmap.createBitmap(width,height,Bitmap.Config.ARGB_8888);long replayCrc;
   try{new Canvas(replay).drawPicture(decoded);ByteBuffer raw=ByteBuffer.allocate(width*height*4);replay.copyPixelsToBuffer(raw);java.util.zip.CRC32 crc=new java.util.zip.CRC32();crc.update(raw.array());replayCrc=crc.getValue();}finally{replay.recycle();}
   System.out.println("BUFFER_BRIDGE_CONSUMER_JSON "+new JSONObject().put("pid",android.os.Process.myPid()).put("producerPid",producerPid).put("width",width).put("height",height).put("received",count).put("crcErrors",crcErrors).put("bufferIdErrors",idErrors).put("sequenceErrors",sequenceErrors).put("alphaBandSampleErrors",alphaErrors).put("distinctRgbaCrc",crcs.size()).put("distinctBufferIds",ids.size()).put("wallMs",(System.nanoTime()-start)/1e6).put("skpBytes",pictureBytes).put("skpWidth",decoded.getWidth()).put("skpHeight",decoded.getHeight()).put("skpReplayMatchesLastSourceCrc",replayCrc==expectedPictureCrc).put("pngTransport",false).put("targetAppAccessed",false));
  }finally{System.out.println("BUFFER_BRIDGE_CONSUMER_RELEASED");}
 }
 public static void main(String[] args)throws Exception{
  Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
  if(android.os.Process.myUid()!=2000||args.length<2||args.length>4||!args[1].matches("rc25_buffer_[a-f0-9]{32}"))throw new IllegalArgumentException();
  Thread watchdog=new Thread(()->{try{Thread.sleep(40000);}catch(InterruptedException e){return;}System.out.println("BUFFER_BRIDGE_HARD_DEADLINE");Runtime.getRuntime().halt(2);});watchdog.setDaemon(true);watchdog.start();
  Looper.prepareMainLooper();Workarounds.apply();System.load("/data/local/tmp/librc25_bufferbridge.so");
  if(args[0].equals("producer"))producer(args[1],args.length>2?Integer.parseInt(args[2]):1440,args.length>3?Integer.parseInt(args[3]):2560);else if(args[0].equals("consumer"))consumer(args[1]);else throw new IllegalArgumentException();
  watchdog.interrupt();System.exit(0);
 }
}
