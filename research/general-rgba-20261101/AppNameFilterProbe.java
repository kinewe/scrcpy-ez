package com.genymobile.scrcpy;

import android.content.Context;
import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.os.*;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import java.io.*;
import java.lang.reflect.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.Consumer;
import java.util.zip.CRC32;
import java.nio.charset.StandardCharsets;

/** Capture-only trial. No View setters, background changes, or Activity conversion. */
public final class AppNameFilterProbe {
    static final Class<?>[] NONE=new Class<?>[0];
    static Object call(Object o,String n,Class<?>[] t,Object...a)throws Exception{return o.getClass().getMethod(n,t).invoke(o,a);}
    static void put(String n,Object v)throws Exception{Field f=NativeAlphaProbe.class.getDeclaredField(n);f.setAccessible(true);f.set(null,v);}
    static Object get(Object o,String n)throws Exception{Field f=o.getClass().getDeclaredField(n);f.setAccessible(true);return f.get(o);}
    static List<NativeAlphaProbe.Node> tree(IBinder window)throws Exception{
        // A full main-page hierarchy may exceed the product engine's 350ms decision
        // budget. This independent diagnostic read has a bounded 1500ms timeout.
        String text=new String(WindowProbe.command(window,"DUMP","",8*1024*1024,1500),StandardCharsets.UTF_8);
        List<NativeAlphaProbe.Node> out=new ArrayList<>(),stack=new ArrayList<>();
        for(String line:text.split("\n")){String trim=line.stripLeading(),id=trim.split(" ",2)[0];if(!id.matches("[A-Za-z0-9_.$]+@[0-9a-fA-F]+"))continue;NativeAlphaProbe.Node n=new NativeAlphaProbe.Node();n.id=id;n.line=line;n.depth=line.length()-trim.length();while(!stack.isEmpty()&&stack.get(stack.size()-1).depth>=n.depth)stack.remove(stack.size()-1);n.parent=stack.isEmpty()?null:stack.get(stack.size()-1);stack.add(n);out.add(n);}
        if(out.isEmpty())throw new IOException("empty view hierarchy");return out;
    }
    static String stats(byte[] rgba,int w,int h){
        int min=255,max=0;long zero=0,mid=0,opaque=0,cp=0,ct=0;CRC32 crc=new CRC32();
        for(int y=0;y<h;y++)for(int x=0;x<w;x++){int a=rgba[(y*w+x)*4+3]&255;crc.update(a);min=Math.min(min,a);max=Math.max(max,a);if(a==0)zero++;else if(a==255)opaque++;else mid++;if(x>=w/4&&x<3*w/4&&y>=h/4&&y<3*h/4){ct++;if(a<255)cp++;}}
        return "min="+min+" max="+max+" zero="+zero+" mid="+mid+" opaque="+opaque+" centerPartial="+cp+" centerTotal="+ct+" alphaCRC="+crc.getValue();
    }
    static byte[] pixels(Bitmap b){int w=b.getWidth(),h=b.getHeight();int[] p=new int[w*h];b.getPixels(p,0,w,0,0,w,h);byte[] out=new byte[p.length*4];for(int i=0;i<p.length;i++){out[i*4]=(byte)(p[i]>>16);out[i*4+1]=(byte)(p[i]>>8);out[i*4+2]=(byte)p[i];out[i*4+3]=(byte)(p[i]>>>24);}return out;}
    @SuppressWarnings("unchecked") public static void main(String[] args)throws Exception{
        Thread.setDefaultUncaughtExceptionHandler((thread,error)->{error.printStackTrace(System.out);System.out.flush();Runtime.getRuntime().halt(1);});
        if(android.os.Process.myUid()!=2000||args.length<1||args.length>4)throw new IllegalArgumentException();
        String pkg=args[0];int count=args.length>=2?Integer.parseInt(args[1]):60;if(count<1||count>3000)throw new IllegalArgumentException();
        boolean mirror=args.length>=3&&"mirror".equals(args[2]);
        LocalServerSocket server=null;LocalSocket socket=null;DataOutputStream wire=null;
        if(args.length==4){if(!args[3].matches("rc25_named_[a-z0-9]+"))throw new IllegalArgumentException();server=new LocalServerSocket(args[3]);System.out.println("WIRE_LISTENING pid="+android.os.Process.myPid());System.out.flush();socket=server.accept();wire=new DataOutputStream(socket.getOutputStream());}
        Looper.prepareMainLooper();Workarounds.apply();Context ctx=FakeContext.get();int uid=ctx.getPackageManager().getApplicationInfo(pkg,0).uid;
        Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);put("pkg",pkg);put("uid",uid);put("atm",atm);
        Object listener=Class.forName("android.window.WindowInfosListenerForTest").getConstructor().newInstance();put("listener",listener);
        Field wf=NativeAlphaProbe.class.getDeclaredField("windows");wf.setAccessible(true);AtomicReference<List<?>> windows=(AtomicReference<List<?>>)wf.get(null);CountDownLatch ready=new CountDownLatch(1);
        Consumer<List<?>> consumer=list->{windows.set(list);if(!list.isEmpty())ready.countDown();};call(listener,"addWindowInfosListener",new Class[]{Consumer.class},consumer);ready.await(2,TimeUnit.SECONDS);
        NativeAlphaScene scene=null;Object ownedMirror=null;NativeAlphaReadback readback=new NativeAlphaReadback();NativeAlphaFrameCodec codec=new NativeAlphaFrameCodec();
        try{
            Method select=NativeAlphaProbe.class.getDeclaredMethod("select");select.setAccessible(true);Object target=select.invoke(null);if(target==null)throw new IOException("no unique target");
            IBinder window=(IBinder)get(target,"token");int display=(Integer)get(target,"display");String activity=(String)get(target,"activity");
            List<NativeAlphaProbe.Node> nodes=tree(window);
            NativeAlphaProbe.Node root=nodes.get(0),camera=null;
            for(NativeAlphaProbe.Node n:nodes)if(n.id.contains("SurfaceView@")&&NativeAlphaPolicy.previewLeaf(nodes,n)){if(camera!=null)throw new IOException("multiple previews");camera=n;}
            if(camera==null)throw new IOException("no independent SurfaceView");
            List<NativeAlphaLayers.Layer> first=NativeAlphaLayers.decode(NativeAlphaLayers.snapshot());Thread.sleep(40);List<NativeAlphaLayers.Layer> second=NativeAlphaLayers.decode(NativeAlphaLayers.snapshot());
            NativeAlphaLayers.Layer a=NativeAlphaLayers.find(first,pkg+"/"+activity,uid,camera.id),b=NativeAlphaLayers.find(second,pkg+"/"+activity,uid,camera.id);
            if(a==null||b==null||a.id!=b.id||a.parent!=b.parent||a.relative!=b.relative||a.z!=b.z)throw new IOException("unstable camera identity");
            int w=root.number("layout:getWidth()"),h=root.number("layout:getHeight()");
            System.out.println("TARGET package="+pkg+" activity="+activity+" display="+display+" size="+w+"x"+h+" view="+camera.id+" alpha="+camera.property("drawing:getAlpha()")+" z="+a.z+" cameraLayer="+a.name);
            Object source;Class<?> sc=Class.forName("android.view.SurfaceControl");
            if(mirror){
                Object wm=Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null);
                ownedMirror=sc.getConstructor().newInstance();if(!Boolean.TRUE.equals(call(wm,"mirrorDisplay",new Class[]{int.class,sc},display,ownedMirror)))throw new IOException("mirrorDisplay rejected");
                Object tx=Class.forName("android.view.SurfaceControl$Transaction").getConstructor().newInstance();try{call(tx,"setLayerStack",new Class[]{sc,int.class},ownedMirror,2147483633);call(tx,"show",new Class[]{sc},ownedMirror);call(tx,"apply",NONE);}finally{call(tx,"close",NONE);}source=ownedMirror;Thread.sleep(150);
            }else{scene=new NativeAlphaScene(atm,window,display,uid,pkg,activity,new AtomicBoolean(),false);scene.rebind();source=scene.source();}
            Class<?> builderClass=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder"),captureClass=Class.forName("android.window.ScreenCapture");
            int sourceId=(Integer)call(source,"getLayerId",NONE);String sourceName=null;
            Set<Integer> descendants=new HashSet<>();descendants.add(a.id);boolean grew=true;
            while(grew){grew=false;for(NativeAlphaLayers.Layer l:second)if(descendants.contains(l.parent)&&descendants.add(l.id))grew=true;}
            List<String> cameraNames=new ArrayList<>();for(NativeAlphaLayers.Layer l:second){if(l.id==sourceId)sourceName=l.name;if(descendants.contains(l.id)){if(l.uid!=uid)throw new IOException("foreign camera descendant");cameraNames.add(l.name);}if(l.uid==uid)System.out.println("APP_LAYER id="+l.id+" parent="+l.parent+" z="+l.z+" name="+l.name);}
            if(mirror)for(NativeAlphaLayers.Layer l:second)if(l.id==a.relative)sourceName=l.name;
            if(sourceName==null||cameraNames.isEmpty())throw new IOException("missing scoped names");
            Object builder=builderClass.getConstructor(sc).newInstance(source);call(builder,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,w,h));call(builder,"setChildrenOnly",new Class[]{boolean.class},false);
            if(mirror)call(builder,"setUid",new Class[]{long.class},(long)uid);
            Object baseline=call(builder,"build",NONE);long total=0;long[] durations=new long[count];Set<Long> crcs=new HashSet<>();
            for(int i=-3;i<count;i++){
                if(!window.isBinderAlive())throw new IOException("target removed");Object argsNow=baseline;
                if(i>-3){int mode=i>=0&&wire==null?2:1;String[] names=i==-2?new String[]{a.name}:i==-1||wire!=null?cameraNames.toArray(new String[0]):new String[]{sourceName};call(builder,"setCaptureMode",new Class[]{int.class},mode);call(builder,"setExcludeOrIncludeLayerNames",new Class[]{String[].class},(Object)names);argsNow=call(builder,"build",NONE);}
                long at=System.nanoTime();Object capture=captureClass.getMethod("captureLayers",argsNow.getClass()).invoke(null,argsNow);if(capture==null)throw new IOException("capture null");
                try{Bitmap hardware=(Bitmap)call(capture,"asBitmap",NONE);try{
                    NativeAlphaReadback.Frame f=readback.read(hardware);long elapsed=System.nanoTime()-at;
                    if(i>=0&&wire!=null){int minA=255,maxA=0;for(int p=3;p<f.rgba.length;p+=4*97){int av=f.rgba[p]&255;minA=Math.min(minA,av);maxA=Math.max(maxA,av);}if(minA==255||maxA==0||!f.alphaExact)throw new IOException("transparent source validation lost");}
                    if(i<0)System.out.println((i==-3?"UNFILTERED":i==-2?"EXCLUDE_CONTAINER":"EXCLUDE_SUBTREE_NAMES")+" "+stats(f.rgba,w,h));
                    else{CRC32 crc=new CRC32();crc.update(f.rgba);crcs.add(crc.getValue());if(wire!=null){codec.prepare(f.rgba,w,h,i==0||i%120==0);wire.writeInt(0x52474c31);wire.writeInt(w);wire.writeInt(h);wire.writeInt(i+1);wire.writeInt(codec.flags);wire.writeInt(codec.packed.size());wire.writeInt((int)crc.getValue());wire.writeInt(f.rgba.length);codec.packed.writeTo(wire);wire.flush();codec.commit();}durations[i]=System.nanoTime()-at;total+=durations[i];if(i<2||i==count-1)System.out.println("FILTERED frame="+(i+1)+" "+stats(f.rgba,w,h)+" alphaExact="+f.alphaExact+" backend="+f.backend+" captureReadMs="+elapsed/1000000.0+" iterationMs="+durations[i]/1000000.0);}
                }finally{hardware.recycle();}}finally{((HardwareBuffer)call(capture,"getHardwareBuffer",NONE)).close();}
            }
            Arrays.sort(durations);System.out.println("CONTINUOUS source="+(mirror?"display-mirror-uid":"window-root-sync")+" strategy="+(wire==null?"include-ui-layer-only":"exclude-camera-subtree-names")+" frames="+count+" medianIterationMs="+durations[count/2]/1000000.0+" serialIterationFPS="+(count*1e9/total)+" distinctRgbaCRC="+crcs.size()+" includesEncoding="+(wire!=null)+" includesHostTransport="+(wire!=null));
            byte[] png=WindowProbe.command(window,"CAPTURE",root.id,24*1024*1024,3500);Bitmap redraw=BitmapFactory.decodeByteArray(png,0,png.length);if(redraw==null)throw new IOException("redraw null");try{System.out.println("UI_REDRAW "+stats(pixels(redraw),w,h));}finally{redraw.recycle();}
            List<NativeAlphaProbe.Node> after=tree(window);NativeAlphaProbe.Node same=null;for(NativeAlphaProbe.Node n:after)if(n.id.equals(camera.id))same=n;
            NativeAlphaLayers.Layer last=NativeAlphaLayers.find(NativeAlphaLayers.decode(NativeAlphaLayers.snapshot()),pkg+"/"+activity,uid,camera.id);
            boolean untouched=same!=null&&Objects.equals(same.property("drawing:getAlpha()"),camera.property("drawing:getAlpha()"))&&last!=null&&last.id==a.id&&last.z==a.z;
            System.out.println("SOURCE_UNMODIFIED="+untouched+" viewSetters=0 activityConversions=0 backgroundMutations=0");if(!untouched)throw new IOException("source changed during read-only trial");
        }finally{codec.close();readback.close();if(scene!=null)scene.close(false);if(ownedMirror!=null)call(ownedMirror,"release",NONE);call(listener,"removeWindowInfosListener",new Class[]{Consumer.class},consumer);if(wire!=null){wire.writeInt(0x52474c30);wire.flush();wire.close();}if(socket!=null)socket.close();if(server!=null)server.close();System.out.println("PROBE_RESOURCES_RELEASED");}
        System.exit(0);
    }
}
