package com.genymobile.scrcpy;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.hardware.HardwareBuffer;
import android.os.*;
import java.lang.reflect.*;
import java.nio.ByteBuffer;
import java.util.*;
import java.util.concurrent.*;
import org.json.*;

/** Independent read-only TextureView experiment. Never changes target View properties. */
public final class TexturePathProbe {
    static final Class<?>[] NONE = new Class<?>[0];

    static JSONObject scrollCapture(IBinder window) throws Exception {
        Class<?> response = Class.forName("android.view.ScrollCaptureResponse");
        Class<?> callbackType = Class.forName("android.view.IScrollCaptureResponseListener");
        JSONObject result = new JSONObject();
        CountDownLatch received = new CountDownLatch(1);
        Binder callback = new Binder() {
            { attachInterface(null, "android.view.IScrollCaptureResponseListener"); }
            @Override protected boolean onTransact(int code, Parcel data, Parcel reply, int flags) throws RemoteException {
                if (code != IBinder.FIRST_CALL_TRANSACTION) return super.onTransact(code, data, reply, flags);
                Object value = null;
                try {
                    data.enforceInterface("android.view.IScrollCaptureResponseListener");
                    value = data.readTypedObject((Parcelable.Creator<?>) response.getField("CREATOR").get(null));
                    synchronized (result) {
                        result.put("connected", SceneContext.call(value, "isConnected", NONE));
                        result.put("description", SceneContext.call(value, "getDescription", NONE));
                        result.put("connectionClass", String.valueOf(SceneContext.call(value, "getConnection", NONE)));
                    }
                } catch (Throwable e) {
                    try { synchronized (result) { result.put("error", e.toString()); } } catch (Exception ignored) {}
                } finally {
                    if (value != null) try { SceneContext.call(value, "close", NONE); } catch (Exception ignored) {}
                    received.countDown();
                }
                return true;
            }
        };
        Object remote = Class.forName("android.view.IWindow$Stub").getMethod("asInterface", IBinder.class).invoke(null, window);
        Object listener = Class.forName("android.view.IScrollCaptureResponseListener$Stub").getMethod("asInterface", IBinder.class).invoke(null, callback);
        SceneContext.call(remote, "requestScrollCapture", new Class[]{callbackType}, listener);
        result.put("callbackReceived", received.await(4, TimeUnit.SECONDS));
        return result;
    }

    public static void main(String[] args) throws Exception {
        Thread.setDefaultUncaughtExceptionHandler((t,e) -> {e.printStackTrace(System.out); System.out.flush(); Runtime.getRuntime().halt(1);});
        if (android.os.Process.myUid() != 2000 || args.length < 1) throw new IllegalArgumentException("shell package [frames]");
        Looper.prepareMainLooper(); Workarounds.apply();
        try (SceneContext scene = new SceneContext(args[0], false); NativeAlphaReadback reader = new NativeAlphaReadback(true)) {
            boolean preview = false;
            for (NativeAlphaProbe.Node n : scene.nodes) if (n.id.contains("TextureView@") && n.number("layout:getWidth()") > 0 && n.number("layout:getHeight()") > 0 && "VISIBLE".equals(n.property("getVisibility()"))) preview = true;
            if (!preview || !scene.activity.endsWith("MainCaptureActivity")) throw new IllegalStateException("not an active TextureView scanner scene: " + scene.activity);
            JSONObject result = scene.metadata();
            JSONArray api = new JSONArray();
            for (String name : new String[]{"android.view.IWindow", "android.view.ViewDebug", "android.view.TextureView", "android.graphics.RenderNode", "android.graphics.HardwareRenderer"}) {
                for (Method m : Class.forName(name).getDeclaredMethods()) {
                    if (m.getName().matches("(?i).*(capture|renderingcommands|surface|opaque|bitmap).*")) api.put(m.toString());
                }
            }
            result.put("api", api);
            int frames = args.length > 1 ? Integer.parseInt(args[1]) : 30;
            if (frames < 1 || frames > 300) throw new IllegalArgumentException("frame limit");
            Object captureArgs = scene.args(null);
            long[] times = new long[frames];
            Set<Long> crcs = new HashSet<>();
            JSONObject stats = null;
            long begin = System.nanoTime();
            for (int i = 0; i < frames; i++) {
                long at = System.nanoTime();
                Object captured = scene.capture(captureArgs);
                if (captured == null) throw new IllegalStateException("null capture");
                try {
                    Bitmap hardware = (Bitmap) SceneContext.call(captured, "asBitmap", NONE);
                    try {
                        NativeAlphaReadback.Frame frame = reader.read(hardware);
                        times[i] = System.nanoTime() - at;
                        stats = SceneContext.alpha(frame.rgba, scene.width, scene.height);
                        java.util.zip.CRC32 crc = new java.util.zip.CRC32(); crc.update(frame.rgba); crcs.add(crc.getValue());
                        if (i == 0 || i == frames-1) System.out.println("NATIVE_FRAME " + i + " " + stats + " ms=" + times[i]/1e6 + " timestamp=" + frame.imageTimestamp);
                    } finally { hardware.recycle(); }
                } finally { ((HardwareBuffer) SceneContext.call(captured, "getHardwareBuffer", NONE)).close(); }
            }
            long wall = System.nanoTime() - begin;
            Arrays.sort(times);
            result.put("nativeCapture", new JSONObject().put("frames", frames).put("wallMs", wall/1e6).put("fpsIncludingStats", frames*1e9/wall).put("medianCaptureReadMs", times[frames/2]/1e6).put("distinctRgbaCrc", crcs.size()).put("lastAlpha", stats).put("includesHostTransport", false));
            // PNG is a diagnostic control only, never the proposed real-time transport.
            JSONArray redraws = new JSONArray();
            for (int i = 0; i < 3; i++) {
                long at = System.nanoTime();
                byte[] png = WindowProbe.command(scene.window, "CAPTURE", scene.nodes.get(0).id, 32*1024*1024, 6000);
                Bitmap bitmap = BitmapFactory.decodeByteArray(png, 0, png.length);
                if (bitmap == null) throw new IllegalStateException("PNG redraw missing");
                try {
                    byte[] rgba = new byte[bitmap.getWidth()*bitmap.getHeight()*4]; bitmap.copyPixelsToBuffer(ByteBuffer.wrap(rgba));
                    redraws.put(SceneContext.alpha(rgba, bitmap.getWidth(), bitmap.getHeight()).put("bytes", png.length).put("requestDecodeStatsMs", (System.nanoTime()-at)/1e6));
                } finally { bitmap.recycle(); }
            }
            result.put("diagnosticRedraws", redraws);
            JSONArray subtreeRedraws = new JSONArray();
            for (NativeAlphaProbe.Node n : scene.nodes) {
                if (!(n.id.contains("NScanTopView@") || n.id.contains("NScanMaskLayerLayout@"))) continue;
                long at = System.nanoTime();
                byte[] png = WindowProbe.command(scene.window, "CAPTURE", n.id, 32*1024*1024, 6000);
                Bitmap bitmap = BitmapFactory.decodeByteArray(png, 0, png.length);
                if (bitmap == null) throw new IllegalStateException("subtree redraw missing");
                try {
                    byte[] rgba = new byte[bitmap.getWidth()*bitmap.getHeight()*4]; bitmap.copyPixelsToBuffer(ByteBuffer.wrap(rgba));
                    subtreeRedraws.put(SceneContext.alpha(rgba,bitmap.getWidth(),bitmap.getHeight()).put("view",n.id).put("width",bitmap.getWidth()).put("height",bitmap.getHeight()).put("bytes",png.length).put("requestDecodeStatsMs",(System.nanoTime()-at)/1e6));
                } finally { bitmap.recycle(); }
            }
            result.put("diagnosticSubtreeRedraws", subtreeRedraws);
            try { result.put("scrollCapture", scrollCapture(scene.window)); } catch (Throwable e) { result.put("scrollCaptureError", e.toString()); }
            result.put("viewMutations", 0).put("activityConversions", 0).put("actualCameraOwnershipVerifiedByThisProbe", false);
            System.out.println("TEXTURE_PATH_JSON " + result);
        } finally { System.out.println("TEXTURE_PATH_RESOURCES_RELEASED"); }
        System.exit(0);
    }
}
