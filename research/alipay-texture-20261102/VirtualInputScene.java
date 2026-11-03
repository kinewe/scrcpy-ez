package com.genymobile.scrcpy;

import android.graphics.*;
import android.hardware.display.VirtualDisplay;
import android.media.*;
import android.net.*;
import android.os.*;
import android.view.Surface;
import com.genymobile.scrcpy.video.PerspectiveCamera;
import java.io.*;
import java.nio.ByteBuffer;
import java.util.concurrent.atomic.AtomicBoolean;

/** Product virtual camera with a deterministic on-device AVC input instead of desktop capture. */
public final class VirtualInputScene {
    public static void main(String[] args) throws Exception {
        if (android.os.Process.myUid() != 2000 || args.length != 1) throw new IllegalArgumentException("shell package");
        Looper.prepareMainLooper(); Workarounds.apply();
        int pid = android.os.Process.myPid();
        String socketName = "yinmo_texture_input_" + pid;
        File stop = new File("/data/local/tmp/yinmo-texture-stop-" + pid);
        AtomicBoolean alive = new AtomicBoolean(true);
        MediaCodec encoder = null;
        HardwareRenderer renderer = null;
        Surface input = null;
        ImageReader sink = null;
        VirtualDisplay display = null;
        PerspectiveCamera camera = null;
        IBinder wake = null;
        Thread producer = null;
        String ownedMac = PerspectiveCamera.associationMac(pid ^ 0x5a000000);
        try (LocalServerSocket server = new LocalServerSocket(socketName); LocalSocket writer = new LocalSocket()) {
            writer.connect(new LocalSocketAddress(socketName));
            LocalSocket receiver = server.accept();
            DataOutputStream stream = new DataOutputStream(writer.getOutputStream());
            stream.writeInt(0x455a5643); stream.writeInt(1920); stream.writeInt(1080); stream.flush();
            camera = new PerspectiveCamera(pid ^ 0x5a000000, args[0], receiver, mac -> System.out.println("OWNED_ASSOCIATION " + mac), false);
            MediaFormat format = MediaFormat.createVideoFormat("video/avc", 1920, 1080);
            format.setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface);
            format.setInteger(MediaFormat.KEY_BIT_RATE, 4000000);
            format.setInteger(MediaFormat.KEY_FRAME_RATE, 30);
            format.setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 1);
            encoder = MediaCodec.createEncoderByType("video/avc");
            encoder.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE);
            input = encoder.createInputSurface(); encoder.start();
            renderer = new HardwareRenderer(); renderer.setOpaque(true); renderer.setSurface(input);
            RenderNode node = new RenderNode("yinmo-owned-camera-input"); node.setPosition(0,0,1920,1080); renderer.setContentRoot(node);
            MediaCodec codec = encoder; HardwareRenderer gpu = renderer;
            producer = new Thread(() -> {
                try {
                    long seq = 0, next = System.nanoTime();
                    byte[] codecConfig = new byte[0];
                    MediaCodec.BufferInfo info = new MediaCodec.BufferInfo();
                    while (alive.get()) {
                        RecordingCanvas canvas = node.beginRecording();
                        canvas.drawColor(Color.rgb(48+(int)(seq%64), 80, 112));
                        Paint p = new Paint(); p.setColor(Color.LTGRAY); canvas.drawRect(100,100,400,250,p);
                        node.endRecording(); gpu.createRenderRequest().setWaitForPresent(true).syncAndDraw();
                        int index;
                        while ((index = codec.dequeueOutputBuffer(info, 1000)) >= 0) {
                            ByteBuffer data = codec.getOutputBuffer(index); byte[] bytes = new byte[info.size];
                            data.position(info.offset); data.get(bytes);
                            if ((info.flags & MediaCodec.BUFFER_FLAG_CODEC_CONFIG) != 0) { codecConfig = bytes; codec.releaseOutputBuffer(index, false); continue; }
                            boolean key = (info.flags & MediaCodec.BUFFER_FLAG_KEY_FRAME) != 0;
                            int prefix = key ? codecConfig.length : 0;
                            stream.writeInt(prefix + bytes.length); stream.writeLong(seq++);
                            if (prefix > 0) stream.write(codecConfig);
                            stream.write(bytes); stream.flush();
                            codec.releaseOutputBuffer(index, false);
                            if (seq % 60 == 0) { System.out.println("OWNED_INPUT packets="+seq); System.out.flush(); }
                        }
                        next += 33333333L; long delay = next-System.nanoTime(); if (delay > 0) Thread.sleep(delay/1000000L);
                    }
                } catch (Throwable e) { e.printStackTrace(System.out); alive.set(false); }
            }, "owned-synthetic-input");
            producer.start();
            sink = ImageReader.newInstance(1440,3200,PixelFormat.RGBA_8888,3);
            HandlerThread drainThread = new HandlerThread("owned-display-drain"); drainThread.start();
            sink.setOnImageAvailableListener(r -> {try (Image image = r.acquireLatestImage()) {} catch (Exception ignored) {}}, new Handler(drainThread.getLooper()));
            try {
                display = camera.createDisplay(1440,3200,600,sink.getSurface(),1|2|8|64|128|1024|16384);
                int id = display.getDisplay().getDisplayId();
                wake = com.genymobile.scrcpy.wrappers.ServiceManager.getPowerManager().acquireScreenWakeLock(id);
                System.out.println("VIRTUAL_SCENE_READY display="+id+" pid="+pid+" stop="+stop+" encoder="+encoder.getName()); System.out.flush();
                java.lang.Process launch = new ProcessBuilder("am","start","--display",String.valueOf(id),"-a","android.intent.action.VIEW","-d","alipays://platformapi/startapp?appId=10000007","-p",args[0]).redirectErrorStream(true).start();
                System.out.write(launch.getInputStream().readAllBytes()); launch.waitFor();
                Thread.sleep(3000);
                java.lang.Process relaunch = new ProcessBuilder("am","start","--display",String.valueOf(id),"-a","android.intent.action.VIEW","-d","alipays://platformapi/startapp?appId=10000007","-p",args[0]).redirectErrorStream(true).start();
                System.out.write(relaunch.getInputStream().readAllBytes()); relaunch.waitFor();
                long deadline = System.nanoTime()+90000000000L;
                while (alive.get() && !stop.exists() && System.nanoTime()<deadline) Thread.sleep(200);
            } finally { drainThread.quitSafely(); }
        } finally {
            alive.set(false);
            if (producer != null) producer.join(3000);
            if (wake != null) com.genymobile.scrcpy.wrappers.ServiceManager.getPowerManager().releaseScreenWakeLock(wake);
            if (display != null) display.release();
            if (sink != null) sink.close();
            if (renderer != null) renderer.destroy();
            if (encoder != null) { try { encoder.stop(); } catch (Exception ignored) {} encoder.release(); }
            if (input != null) input.release();
            if (camera != null) camera.close();
            // Also handles a constructor failure after acquiring this unique association.
            PerspectiveCamera.removeAssociation(ownedMac);
            stop.delete();
            System.out.println("VIRTUAL_SCENE_RESOURCES_RELEASED"); System.out.flush();
        }
        System.exit(0);
    }
}
