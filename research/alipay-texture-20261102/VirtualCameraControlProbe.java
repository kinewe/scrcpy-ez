package com.genymobile.scrcpy;
import android.content.*;
import android.graphics.ImageFormat;
import android.hardware.camera2.*;
import android.hardware.camera2.params.StreamConfigurationMap;
import android.media.*;
import android.os.*;
import android.util.Size;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import org.json.*;

/** Own shell Camera2 client, restricted to the explicitly named experimental virtual camera. */
public final class VirtualCameraControlProbe {
    public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((t,e)->{e.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||(args.length!=3&&args.length!=4)||!args[1].matches("v[0-9]+_[0-9]+")||Integer.parseInt(args[2])<=0)throw new IllegalArgumentException("package ownedVirtualCamera ownedDisplay [single|matched|mixed|rgba]");
        String mode=args.length==4?args[3]:"single";if(!Arrays.asList("single","matched","mixed","rgba","deferred-mixed","deferred-matched").contains(mode))throw new IllegalArgumentException("control mode");
        Looper.prepareMainLooper();Workarounds.apply();
        final int device=Integer.parseInt(args[1].substring(1,args[1].indexOf('_')));
        Context context=new ContextWrapper(FakeContext.get()){
            @Override public int getDeviceId(){return device;}
            @Override public AttributionSource getAttributionSource(){return new AttributionSource.Builder(android.os.Process.myUid()).setPackageName(FakeContext.PACKAGE_NAME).setDeviceId(device).build();}
        };
        java.lang.reflect.Constructor<android.hardware.display.DisplayManager> dc=android.hardware.display.DisplayManager.class.getDeclaredConstructor(Context.class);dc.setAccessible(true);
        Object dm=dc.newInstance(FakeContext.get());
        if(((android.hardware.display.DisplayManager)dm).getDisplay(Integer.parseInt(args[2]))==null)throw new SecurityException("owned display absent");
        java.lang.reflect.Constructor<CameraManager> constructor=CameraManager.class.getDeclaredConstructor(Context.class);constructor.setAccessible(true);CameraManager manager=constructor.newInstance(context);
        HandlerThread thread=new HandlerThread("own-virtual-camera-control");thread.start();Handler handler=new Handler(thread.getLooper());
        AtomicReference<CameraDevice> camera=new AtomicReference<>();AtomicReference<CameraCaptureSession> session=new AtomicReference<>();AtomicReference<String> failure=new AtomicReference<>();AtomicInteger images=new AtomicInteger();Set<Long> crcs=ConcurrentHashMap.newKeySet();
        AtomicInteger previewImages=new AtomicInteger();AtomicReference<String> actualPreviewSize=new AtomicReference<>();ImageReader reader=null,preview=null;JSONObject result=new JSONObject().put("virtualCamera",args[1]).put("display",Integer.parseInt(args[2])).put("shellDeviceContext",device).put("physicalCameraFallback",false).put("mode",mode);
        try{
            String[] visible=manager.getCameraIdList();result.put("visibleIds",new JSONArray(java.util.Arrays.asList(visible)));
            String suffix=args[1].substring(args[1].indexOf('_')+1),requested=args[1];
            if(!java.util.Arrays.asList(visible).contains(requested)){
                if(java.util.Arrays.asList(visible).contains(suffix))requested=suffix;
                else{
                    Object vdm=FakeContext.get().getSystemService("virtualdevice");Class<?> policy=Class.forName("android.companion.virtual.VirtualDeviceParams");
                    int custom=policy.getField("DEVICE_POLICY_CUSTOM").getInt(null),kind=policy.getField("POLICY_TYPE_CAMERA").getInt(null);
                    int actual=(Integer)SceneContext.call(vdm,"getDevicePolicy",new Class[]{int.class,int.class},device,kind);
                    result.put("verifiedCameraDevicePolicy",actual);
                    // AOSP maps a virtual device's back camera to local ID 0.
                    // Accept it only for this non-default device with CUSTOM camera policy,
                    // never as a fallback in the physical/default device context.
                    if(device<=0||actual!=custom||visible.length!=1||!"0".equals(visible[0]))throw new SecurityException("owned virtual back camera mapping not established");
                    requested="0";
                }
            }
            result.put("requestedScopedCameraId",requested);
            CameraCharacteristics info=manager.getCameraCharacteristics(requested);StreamConfigurationMap map=info.get(CameraCharacteristics.SCALER_STREAM_CONFIGURATION_MAP);Size[] sizes=map.getOutputSizes(ImageFormat.YUV_420_888);if(sizes==null||sizes.length==0)throw new IllegalStateException("no YUV output");
            Size size=sizes[0];for(Size s:sizes)if(s.getWidth()==1920&&s.getHeight()==1080)size=s;
            int format=mode.equals("rgba")?android.graphics.PixelFormat.RGBA_8888:ImageFormat.YUV_420_888;
            JSONArray advertised=new JSONArray();for(Size available:sizes)advertised.put(available.toString());
            result.put("outputSize",size.toString()).put("outputFormat",format).put("advertisedYuvSizes",advertised);
            reader=ImageReader.newInstance(size.getWidth(),size.getHeight(),format,3);
            java.util.List<android.view.Surface> outputs=new ArrayList<>();outputs.add(reader.getSurface());
            if(mode.equals("matched")||mode.equals("mixed")){
                int width=mode.equals("mixed")?1600:1280;
                preview=ImageReader.newInstance(width,720,ImageFormat.PRIVATE,3,android.hardware.HardwareBuffer.USAGE_GPU_SAMPLED_IMAGE);
                preview.setOnImageAvailableListener(r->{try(Image image=r.acquireLatestImage()){if(image!=null){actualPreviewSize.set(image.getWidth()+"x"+image.getHeight());previewImages.incrementAndGet();}}},handler);
                outputs.add(preview.getSurface());result.put("privatePreviewSize",width+"x720");
            }
            reader.setOnImageAvailableListener(r->{try(Image image=r.acquireLatestImage()){if(image==null)return;java.nio.ByteBuffer b=image.getPlanes()[0].getBuffer();java.util.zip.CRC32 crc=new java.util.zip.CRC32();int limit=Math.min(b.remaining(),65536);for(int i=0;i<limit;i++)crc.update(b.get()&255);crcs.add(crc.getValue());images.incrementAndGet();}},handler);
            CountDownLatch opened=new CountDownLatch(1);
            manager.openCamera(requested,new CameraDevice.StateCallback(){public void onOpened(CameraDevice c){camera.set(c);opened.countDown();}public void onDisconnected(CameraDevice c){failure.set("disconnected");c.close();opened.countDown();}public void onError(CameraDevice c,int e){failure.set("open_error_"+e);c.close();opened.countDown();}},handler);
            if(!opened.await(8,TimeUnit.SECONDS)||camera.get()==null)throw new IllegalStateException("open: "+failure.get());
            CountDownLatch configured=new CountDownLatch(1);
            CameraCaptureSession.StateCallback callback=new CameraCaptureSession.StateCallback(){public void onConfigured(CameraCaptureSession s){session.set(s);try{CaptureRequest.Builder request=camera.get().createCaptureRequest(CameraDevice.TEMPLATE_PREVIEW);for(android.view.Surface output:outputs)request.addTarget(output);s.setRepeatingRequest(request.build(),null,handler);}catch(Exception e){failure.set(e.toString());}configured.countDown();}public void onConfigureFailed(CameraCaptureSession s){failure.set("configuration_failed");s.close();configured.countDown();}};
            if(mode.startsWith("deferred-")){
                int width=mode.equals("deferred-mixed")?1600:1280;
                java.util.List<android.hardware.camera2.params.OutputConfiguration> configurations=new ArrayList<>();
                configurations.add(new android.hardware.camera2.params.OutputConfiguration(new Size(width,720),android.graphics.SurfaceTexture.class));
                configurations.add(new android.hardware.camera2.params.OutputConfiguration(reader.getSurface()));
                result.put("deferredPrivatePreviewSize",width+"x720");
                camera.get().createCaptureSessionByOutputConfigurations(configurations,callback,handler);
            }else camera.get().createCaptureSession(outputs,callback,handler);
            if(!configured.await(8,TimeUnit.SECONDS)||session.get()==null||failure.get()!=null)throw new IllegalStateException("configure: "+failure.get());
            Thread.sleep(3000);result.put("receivedImages",images.get()).put("distinctSampledYCrc",crcs.size()).put("configured",true);
        }catch(Exception e){result.put("failure",e.toString()).put("receivedImages",images.get()).put("configured",false);}
        finally{if(session.get()!=null)session.get().close();if(camera.get()!=null)camera.get().close();if(reader!=null)reader.close();if(preview!=null)preview.close();thread.quitSafely();thread.join(1000);result.put("receivedPrivatePreviewImages",previewImages.get()).put("actualPrivateImageSize",actualPreviewSize.get());System.out.println("VIRTUAL_CAMERA_CONTROL_JSON "+result);System.out.println("VIRTUAL_CAMERA_CONTROL_RESOURCES_RELEASED");}
        System.exit(0);
    }
}
