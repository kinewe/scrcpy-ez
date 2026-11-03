package com.genymobile.scrcpy;

import android.content.*;
import android.graphics.*;
import android.media.*;
import android.os.*;
import android.view.Surface;
import java.io.*;
import java.lang.reflect.*;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import org.json.*;

/** Two cooperating shell processes, no target app or camera interaction.
 * Consumer owns the ImageReader; producer receives only its Surface over Binder.
 */
public final class SurfaceBridgeProbe {
 static final int W=1440,H=2560,N=36;
 static final String DESCRIPTOR="com.genymobile.scrcpy.rc25.SurfaceBridge";
 static Object manager()throws Exception{return Class.forName("android.app.ActivityManager").getMethod("getService").invoke(null);}
 static Method method(Object o,String name){for(Method m:o.getClass().getMethods())if(m.getName().equals(name)){m.setAccessible(true);return m;}throw new IllegalArgumentException(name);}
 static Object receiver(Binder binder)throws Exception{return Class.forName("android.content.IIntentReceiver$Stub").getMethod("asInterface",IBinder.class).invoke(null,binder);}
 static void send(Object am,Intent intent)throws Exception{
  Object result=method(am,"broadcastIntentWithFeature").invoke(am,null,null,intent,null,null,0,null,null,new String[]{"android.permission.DUMP"},null,null,-1,null,false,false,0);
  System.out.println("BRIDGE_BROADCAST_RESULT "+result);System.out.flush();
 }
 static int value(ByteBuffer plane,int row,int pixel,int x,int y,int channel){return plane.get(y*row+x*pixel+channel)&255;}
 static void consumer(String action)throws Exception{
  ImageReader reader=ImageReader.newInstance(W,H,PixelFormat.RGBA_8888,3);
  HandlerThread thread=new HandlerThread("rc25-owned-surface-consumer");thread.start();
  CountDownLatch completed=new CountDownLatch(1);AtomicInteger count=new AtomicInteger(),sampleErrors=new AtomicInteger();
  Set<Long> crcs=ConcurrentHashMap.newKeySet();Set<Integer> sequences=ConcurrentHashMap.newKeySet();
  AtomicLong first=new AtomicLong(),last=new AtomicLong();AtomicReference<String> failure=new AtomicReference<>();
  reader.setOnImageAvailableListener(r->{
   try(Image image=r.acquireNextImage()){
    if(image==null)return;
    Image.Plane p=image.getPlanes()[0];ByteBuffer data=p.getBuffer();int row=p.getRowStride(),pixel=p.getPixelStride();
    if(pixel!=4)throw new IllegalStateException("unexpected pixel stride");
    long now=System.nanoTime();first.compareAndSet(0,now);last.set(now);
    // Samples are strictly inside flat, non-antialiased bands. This is not a
    // full per-pixel reference comparison and is reported accordingly.
    for(int y=H/8;y<H;y+=H/8){
     if(value(data,row,pixel,W/8,y,3)!=0)sampleErrors.incrementAndGet();
     if(value(data,row,pixel,W*3/8,y,3)!=255)sampleErrors.incrementAndGet();
     if(value(data,row,pixel,W*5/8,y,3)!=128)sampleErrors.incrementAndGet();
    }
    sequences.add(value(data,row,pixel,16,16,1));
    java.util.zip.CRC32 crc=new java.util.zip.CRC32();crc.update(data.duplicate());crcs.add(crc.getValue());
    if(count.incrementAndGet()>=N)completed.countDown();
   }catch(Throwable e){failure.set(e.toString());completed.countDown();}
  },new Handler(thread.getLooper()));
  Surface surface=reader.getSurface();AtomicInteger producerPid=new AtomicInteger();
  Binder endpoint=new Binder(){
   {attachInterface(null,"android.content.IIntentReceiver");}
   @Override protected boolean onTransact(int code,Parcel data,Parcel reply,int flags)throws RemoteException{
    if(code!=IBinder.FIRST_CALL_TRANSACTION)return super.onTransact(code,data,reply,flags);
    data.enforceInterface("android.content.IIntentReceiver");Intent intent=data.readTypedObject(Intent.CREATOR);
    System.out.println("BRIDGE_CONSUMER_RECEIVE action="+(intent==null?"null":intent.getAction()));System.out.flush();
    if(intent==null||!action.equals(intent.getAction()))return true;
    Bundle extra=intent.getBundleExtra("bridge");if(extra==null)return true;IBinder client=extra.getBinder("client");if(client==null)return true;
    Parcel request=Parcel.obtain(),response=Parcel.obtain();
    try{request.writeInterfaceToken(DESCRIPTOR);request.writeString(action);request.writeTypedObject(surface,0);request.writeInt(W);request.writeInt(H);request.writeInt(android.os.Process.myPid());client.transact(IBinder.FIRST_CALL_TRANSACTION,request,response,0);response.readException();producerPid.set(response.readInt());}
    catch(Throwable e){failure.set(e.toString());completed.countDown();}
    finally{request.recycle();response.recycle();}
    return true;
   }
  };
  Object am=manager(),remote=receiver(endpoint);boolean registered=false;
  try{
   method(am,"registerReceiverWithFeature").invoke(am,null,"com.android.shell",null,null,remote,new IntentFilter(action),"android.permission.DUMP",0,Context.RECEIVER_EXPORTED);registered=true;
   System.out.println("BRIDGE_CONSUMER_READY pid="+android.os.Process.myPid()+" action="+action);System.out.flush();
   boolean done=completed.await(25,TimeUnit.SECONDS);
   System.out.println("SURFACE_BRIDGE_CONSUMER_JSON "+new JSONObject().put("pid",android.os.Process.myPid()).put("producerPid",producerPid.get()).put("width",W).put("height",H).put("received",count.get()).put("distinctRawPlaneCrc",crcs.size()).put("distinctSequencePixels",sequences.size()).put("alphaBandSampleErrors",sampleErrors.get()).put("firstToLastMs",(last.get()-first.get())/1e6).put("completed",done).put("failure",failure.get()).put("referenceCheck","flat-band alpha samples, not full-frame equality").put("targetAppAccessed",false));
  }finally{
   if(registered)method(am,"unregisterReceiver").invoke(am,remote);
   reader.close();thread.quitSafely();thread.join(1000);System.out.println("BRIDGE_CONSUMER_RELEASED");
  }
 }
 static void producer(String action)throws Exception{
  AtomicReference<Surface> target=new AtomicReference<>();AtomicInteger consumerPid=new AtomicInteger();CountDownLatch ready=new CountDownLatch(1);
  Binder client=new Binder(){
   {attachInterface(null,DESCRIPTOR);}
   @Override protected boolean onTransact(int code,Parcel data,Parcel reply,int flags)throws RemoteException{
    if(code!=IBinder.FIRST_CALL_TRANSACTION)return super.onTransact(code,data,reply,flags);
    if(Binder.getCallingUid()!=2000)throw new SecurityException("shell peer only");
    data.enforceInterface(DESCRIPTOR);if(!action.equals(data.readString()))throw new SecurityException("wrong experiment");
    Surface surface=data.readTypedObject(Surface.CREATOR);int width=data.readInt(),height=data.readInt(),pid=data.readInt();
    if(width!=W||height!=H||surface==null||!surface.isValid())throw new SecurityException("unexpected surface");
    if(!target.compareAndSet(null,surface)){surface.release();throw new SecurityException("already bound");}
    consumerPid.set(pid);reply.writeNoException();reply.writeInt(android.os.Process.myPid());ready.countDown();return true;
   }
  };
  Bundle extra=new Bundle();extra.putBinder("client",client);Intent intent=new Intent(action).putExtra("bridge",extra).addFlags(Intent.FLAG_RECEIVER_FOREGROUND);
  send(manager(),intent);if(!ready.await(8,TimeUnit.SECONDS))throw new IllegalStateException("owned consumer not reached");
  HardwareRenderer renderer=new HardwareRenderer();RenderNode node=new RenderNode("rc25-owned-precomposition-ui");node.setPosition(0,0,W,H);
  AtomicInteger pictures=new AtomicInteger(),currentSequence=new AtomicInteger();AtomicReference<Picture> saved=new AtomicReference<>();AtomicInteger pictureSequence=new AtomicInteger();
  Class<?> callbackType=Class.forName("android.graphics.HardwareRenderer$PictureCapturedCallback");
  Object callback=Proxy.newProxyInstance(callbackType.getClassLoader(),new Class[]{callbackType},(o,m,a)->{
   if(m.getName().equals("onPictureCaptured")){pictures.incrementAndGet();if(saved.compareAndSet(null,(Picture)a[0]))pictureSequence.set(currentSequence.get());}
   else if(m.getName().equals("hashCode"))return System.identityHashCode(o);else if(m.getName().equals("equals"))return o==a[0];
   return null;
  });
  try{
   HardwareRenderer.class.getMethod("setDebuggingEnabled",boolean.class).invoke(null,false);
   renderer.setOpaque(false);renderer.setSurface(target.get());renderer.setContentRoot(node);
   HardwareRenderer.class.getMethod("setPictureCaptureCallback",callbackType).invoke(renderer,callback);
   Paint paint=new Paint();paint.setAntiAlias(false);long start=System.nanoTime();
   for(int seq=1;seq<=N;seq++){
    currentSequence.set(seq);RecordingCanvas canvas=node.beginRecording();
    paint.setColor(Color.WHITE);canvas.drawRect(W/4,0,W/2,H,paint);
    paint.setColor(Color.argb(128,0,255,0));canvas.drawRect(W/2,0,W*3/4,H,paint);
    paint.setColor(Color.argb(128,255,0,0));canvas.drawRect(W*3/4,seq*8,W,seq*8+128,paint);
    paint.setColor(Color.rgb(0,seq,255));canvas.drawRect(0,0,32,32,paint);
    node.endRecording();renderer.createRenderRequest().setWaitForPresent(true).syncAndDraw();Thread.sleep(20);
   }
   long drawNs=System.nanoTime()-start;Picture picture=saved.get();JSONObject replay=new JSONObject();
   if(picture!=null){
    try(FileOutputStream stream=new FileOutputStream("/data/local/tmp/rc25-renderbridge-owned.skp")){Picture.class.getMethod("writeToStream",OutputStream.class).invoke(picture,stream);}
    Bitmap bitmap=Bitmap.createBitmap(picture,W,H,Bitmap.Config.ARGB_8888);
    try{replay.put("emptyAlpha",Color.alpha(bitmap.getPixel(W/8,H/2))).put("opaqueAlpha",Color.alpha(bitmap.getPixel(W*3/8,H/2))).put("partialAlpha",Color.alpha(bitmap.getPixel(W*5/8,H/2))).put("pictureWidth",picture.getWidth()).put("pictureHeight",picture.getHeight());}finally{bitmap.recycle();}
   }
   System.out.println("SURFACE_BRIDGE_PRODUCER_JSON "+new JSONObject().put("pid",android.os.Process.myPid()).put("consumerPid",consumerPid.get()).put("submitted",N).put("drawMs",drawNs/1e6).put("pictureCallbacks",pictures.get()).put("replayedPictureSequence",pictureSequence.get()).put("replay",replay).put("rendererDebuggingEnabled",false).put("usesPng",false).put("targetAppAccessed",false));
  }finally{
   HardwareRenderer.class.getMethod("setPictureCaptureCallback",callbackType).invoke(renderer,new Object[]{null});renderer.destroy();target.get().release();System.out.println("BRIDGE_PRODUCER_RELEASED");
  }
 }
 public static void main(String[] args)throws Exception{
  Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
  if(android.os.Process.myUid()!=2000||args.length!=2||!args[1].matches("com.genymobile.scrcpy.rc25.BRIDGE.[a-f0-9]{32}"))throw new IllegalArgumentException("consumer|producer uniqueAction");
  Thread watchdog=new Thread(()->{try{Thread.sleep(35000);}catch(InterruptedException e){return;}System.out.println("BRIDGE_HARD_DEADLINE");Runtime.getRuntime().halt(2);});watchdog.setDaemon(true);watchdog.start();
  Looper.prepareMainLooper();Workarounds.apply();
  if(args[0].equals("consumer"))consumer(args[1]);else if(args[0].equals("producer"))producer(args[1]);else throw new IllegalArgumentException("role");
  watchdog.interrupt();System.exit(0);
 }
}
