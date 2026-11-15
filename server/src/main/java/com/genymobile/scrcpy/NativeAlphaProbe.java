package com.genymobile.scrcpy;

import android.content.ComponentName;
import android.graphics.Bitmap;
import android.graphics.Point;
import android.graphics.Rect;
import android.hardware.HardwareBuffer;
import android.hardware.display.DisplayManager;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.os.IBinder;
import android.os.Looper;
import android.os.Parcelable;
import java.io.*;
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.*;
import java.util.function.Consumer;
import java.util.regex.*;
import java.util.zip.*;

/** RC23 native window alpha. Runs from the existing server DEX as shell;
 * never installs an APK, invokes CAPTURE/PNG or changes desktop-camera delivery. */
public final class NativeAlphaProbe {
    private static final int MAX_PIXELS=16*1024*1024;
    private static final AtomicBoolean stop=new AtomicBoolean(),enabled=new AtomicBoolean();
    private static final AtomicBoolean armed=new AtomicBoolean();
    private static final AtomicInteger controlRevision=new AtomicInteger();
    private static final Set<IBinder> unknownRoots=Collections.synchronizedSet(new HashSet<>());
    private static Class<?> sc,tx,argsClass;
    private static Object wm,atm,listener;
    private static DisplayManager displays;
    private static NativeAlphaPolicy policy;
    private static final AtomicBoolean windowChanged=new AtomicBoolean(true);
    private static final Map<IBinder,Long> firstSeen=Collections.synchronizedMap(new LinkedHashMap<>());
    private static final ThreadLocal<Long> commandDeadline=ThreadLocal.withInitial(()->0L);
    private static final ThreadLocal<Integer> commandTimeout=ThreadLocal.withInitial(()->350);
    private static String pkg;
    private static int uid;
    private static final AtomicReference<List<?>> windows=new AtomicReference<>(Collections.emptyList());
    private static Object call(Object o,String name,Class<?>[] types,Object...a)throws Exception{return o.getClass().getMethod(name,types).invoke(o,a);}
    private static Object service(String name,String type)throws Exception{
        IBinder b=(IBinder)Class.forName("android.os.ServiceManager").getMethod("getService",String.class).invoke(null,name);
        return Class.forName(type+"$Stub").getMethod("asInterface",IBinder.class).invoke(null,b);
    }
    private static final class Target {
        IBinder token;String activity;int display;long seenAt;
        Target(IBinder t,String a,int d){token=t;activity=a;display=d;synchronized(firstSeen){if(firstSeen.size()>64)firstSeen.clear();seenAt=firstSeen.computeIfAbsent(t,k->System.nanoTime());}}
    }
    private static String activity(String name){
        return NativeAlphaWindowIdentity.activity(name,pkg);
    }
    private static String lastSelectionState="";
    private static byte[] command(IBinder token,String op,String args,int cap)throws Exception {
        int timeout=commandTimeout.get();
        long deadline=commandDeadline.get();long remaining=deadline==0?timeout:(deadline-System.nanoTime())/1000000L;
        if(remaining<=0)throw new java.util.concurrent.TimeoutException("alpha decision budget");
        try{
            return WindowProbe.command(token,op,args,cap,(int)Math.min(timeout,remaining));
        }catch(java.util.concurrent.TimeoutException error){
            throw new java.util.concurrent.TimeoutException("window_"+op.toLowerCase(java.util.Locale.ROOT)+"_timeout");
        }
    }
    private static boolean top(Target target)throws Exception{
        for(Object t:(List<?>)call(atm,"getAllRootTaskInfos",new Class[]{})){
            if(t.getClass().getField("displayId").getInt(t)!=target.display)continue;
            ComponentName a=(ComponentName)t.getClass().getField("topActivity").get(t);
            if(a!=null&&a.getPackageName().equals(pkg)&&a.getClassName().equals(target.activity))return true;
        }return false;
    }
    static synchronized List<?> rawWindows()throws Exception{
        Field f=listener.getClass().getDeclaredField("mListeners");f.setAccessible(true);
        for(Object nativeListener:((Map<?,?>)f.get(listener)).values()){
            Object pair=call(nativeListener,"register",new Class[]{});
            return Arrays.asList((Object[])pair.getClass().getField("first").get(pair));
        }return Collections.emptyList();
    }
    private static boolean owned(Target target)throws Exception{
        for(Object h:rawWindows()){
            Class<?> c=h.getClass();
            if(c.getField("displayId").getInt(h)!=target.display||c.getField("ownerUid").getInt(h)!=uid)continue;
            IBinder t=(IBinder)call(h,"getWindowToken",new Class[]{});
            if(target.token.equals(t))return true;
        }return false;
    }
    private static Target select()throws Exception{
        List<Target> found=new ArrayList<>();
        for(Object w:windows.get())try{
            Class<?> c=w.getClass();int d=c.getField("displayId").getInt(w);
            if(d<=0||!c.getField("isVisible").getBoolean(w))continue;
            String a=activity((String)c.getField("name").get(w));IBinder token=(IBinder)c.getField("windowToken").get(w);
            if(a!=null&&token!=null){Target t=new Target(token,a,d);if(top(t)&&owned(t))found.add(t);}
        }catch(ReflectiveOperationException ignored){}
        if(found.isEmpty())for(Object w:rawWindows())try{
            Class<?> c=w.getClass();int d=c.getField("displayId").getInt(w);
            if(d<=0||c.getField("ownerUid").getInt(w)!=uid||(c.getField("inputConfig").getInt(w)&2)!=0)continue;
            String a=activity((String)c.getField("name").get(w));IBinder token=(IBinder)call(w,"getWindowToken",new Class[]{});
            if(a!=null&&token!=null){Target t=new Target(token,a,d);if(top(t))found.add(t);}
        }catch(ReflectiveOperationException ignored){}
        // Ambiguous parallel instances must never mutate each other's window.
        String state=found.isEmpty()?"waiting_for_window":found.size()==1?"selected":"ambiguous_windows";
        String selection=state+":"+found.size();
        if(!selection.equals(lastSelectionState)){
            System.out.println("NATIVE_ALPHA_SELECTION state="+state+" candidates="+found.size());
            lastSelectionState=selection;
        }
        return found.size()==1?found.get(0):null;
    }
    static final class Node {
        String id,line;Node parent;int depth;Integer surfaceOrder,backgroundResource;boolean backgroundKnownNull,resourceFromTheme;int sceneFeature=-1;String previewKind;
        String property(String key){int at=line.indexOf(key+"=");if(at<0)return null;at+=key.length()+1;int comma=at;while(comma<line.length()&&line.charAt(comma)>='0'&&line.charAt(comma)<='9')comma++;if(comma==at||comma>=line.length()||line.charAt(comma)!=',')return null;int end=line.indexOf(' ',comma+1);return line.substring(comma+1,end<0?line.length():end);}
        int number(String key){String s=property(key);return s==null?0:Integer.parseInt(s);}
        Integer background(){String s=property("bg_state_mBaseColor");if(s==null)s=property("bg_state_mUseColor");return s==null?null:Integer.valueOf(s);}
    }
    private static List<Node> tree(IBinder token)throws Exception{
        String text=new String(command(token,"DUMP","",8*1024*1024),StandardCharsets.UTF_8);
        List<Node> result=new ArrayList<>(),stack=new ArrayList<>();
        for(String line:text.split("\n")){
            String trim=line.stripLeading(),id=trim.split(" ",2)[0];if(!id.matches("[A-Za-z0-9_.$]+@[0-9a-fA-F]+"))continue;
            Node n=new Node();n.id=id;n.line=line;n.depth=line.length()-trim.length();
            while(!stack.isEmpty()&&stack.get(stack.size()-1).depth>=n.depth)stack.remove(stack.size()-1);
            n.parent=stack.isEmpty()?null:stack.get(stack.size()-1);stack.add(n);result.add(n);
        }return result;
    }
    private static void set(IBinder token,String view,String method,char type,Object value)throws Exception{
        ByteArrayOutputStream bytes=new ByteArrayOutputStream();DataOutputStream a=new DataOutputStream(bytes);
        a.writeInt(1);a.writeChar(type);if(type=='F')a.writeFloat((Float)value);else if(type=='Z')a.writeBoolean((Boolean)value);else a.writeInt((Integer)value);
        String reply=new String(command(token,"INVOKE_METHOD",view+" "+method+" "+android.util.Base64.encodeToString(bytes.toByteArray(),2),65536),StandardCharsets.UTF_8);
        if(!reply.startsWith("1\n"))throw new IOException("View mutation rejected: "+method+" "+view);
    }
    private static final class Camera {
        String id;float alpha;int visibility;Integer originalSubLayer;boolean exactOrder,surface,modified,zalpha;
        Camera(Node n){id=n.id;exactOrder=n.surfaceOrder!=null;originalSubLayer=n.surfaceOrder;if(originalSubLayer==null&&NativeAlphaPolicy.knownSurfaceLayer(n))originalSubLayer=Integer.valueOf(n.property("mSubLayer"));alpha=Float.parseFloat(n.property("drawing:getAlpha()"));String v=n.property("getVisibility()");visibility="VISIBLE".equals(v)?0:"GONE".equals(v)?8:"INVISIBLE".equals(v)?4:Integer.parseInt(v);surface=n.id.contains("SurfaceView");}
    }
    private static final class Session implements AutoCloseable {
        final Target target;
        final LinkedHashMap<String,Integer> colours=new LinkedHashMap<>();
        final Map<String,Node> snapshots=new HashMap<>();final Map<String,Integer> resources=new LinkedHashMap<>();
        final AtomicBoolean running=new AtomicBoolean(true);
        volatile boolean ready;volatile Exception failure;volatile long revision;
        String root;boolean rootUnknown,applied;volatile boolean suspended;Camera camera;Object mirror;Thread monitor;
        NativeAlphaPolicy.Plan plan;
        String viewportKey;
        String monitoredViewport;
        long monitoredRevision=-1;
        Map<String,Map<String,Object>> monitoredLayout;
        long lastTreeAt;
        long sceneSelectionGraceUntil;
        NativeAlphaScene scene,restoreScope;boolean sceneAttempted,windowAttempted,directSource,sourceRetried,generationReplaced;
        Session(Target t)throws Exception{
            target=t;commandDeadline.set(System.nanoTime()+900000000L);
            try{
                policy.refreshApplication();android.util.DisplayMetrics metrics=new android.util.DisplayMetrics();displays.getDisplay(t.display).getRealMetrics(metrics);policy.viewport(metrics);
                Object holder=sc.getConstructor().newInstance();
                if(!(Boolean)call(wm,"mirrorDisplay",new Class[]{int.class,sc},t.display,holder))throw new IOException("mirrorDisplay rejected");
                mirror=holder;Object transaction=tx.getConstructor().newInstance();
                try{call(transaction,"setLayerStack",new Class[]{sc,int.class},holder,2147483638);call(transaction,"show",new Class[]{sc},holder);call(transaction,"apply",new Class[]{});}finally{call(transaction,"close",new Class[]{});}
                refresh();
                System.out.println("NATIVE_ALPHA_BOUND uid="+uid+" display="+t.display+" activity="+t.activity+" camera="+camera.id+" cache="+plan.cache);
            }catch(Exception e){close(false);throw e;}finally{commandDeadline.set(0L);}
        }

        void startLayoutMonitor() {
            if (monitor != null) return;
            // Initial binding and scene activation already check the hierarchy.
            // Start periodic checks after the first verified frame is sent so
            // their UI-thread dumps cannot compete with scanner startup.
            monitor = new Thread(() -> {
                int misses = 0;
                long timeoutSince = 0;
                while (running.get() && !stop.get()) {
                    try {
                        long checkedRevision = revision;
                        boolean stable = ready && !suspended && enabled.get() && misses == 0 && timeoutSince == 0;
                        for (long remaining = stable ? 1500 : 500; remaining > 0; remaining -= 250) {
                            Thread.sleep(250);
                            if (!running.get() || stop.get() || revision != checkedRevision || !ready) break;
                        }
                        if (!running.get() || stop.get()) break;
                        commandDeadline.set(System.nanoTime() + 450000000L);
                        try {
                            refresh(true);
                        } finally {
                            commandDeadline.remove();
                        }
                        misses = 0;
                        timeoutSince = 0;
                    } catch (InterruptedException e) {
                        break;
                    } catch (java.util.concurrent.TimeoutException e) {
                        long now = System.nanoTime();
                        if (timeoutSince == 0) timeoutSince = now;
                        // Retain only an already verified, unmodified binding. A refresh
                        // that failed after changing the layout must recover immediately.
                        if (!ready || now - timeoutSince >= 2000000000L) {
                            failure = e;
                            break;
                        }
                    } catch (Exception e) {
                        timeoutSince = 0;
                        if (e instanceof NativeAlphaPolicy.Rejected || ++misses >= 2) {
                            failure = e;
                            break;
                        }
                    }
                }
            }, "native-alpha-layout");
            monitor.setDaemon(true);
            monitor.start();
        }
        boolean refresh()throws Exception{return refresh(false);}

        private Map<String,Map<String,Object>> captureLayout()throws Exception{
            Map<String,Map<String,Object>> views=NativeAlphaSnapshot.encoded(target.token);
            Set<String> bindings=new HashSet<>();
            for(Node node=plan.camera;node!=null;node=node.parent)bindings.add(node.id);
            for(Node node:plan.backgrounds.values())bindings.add(node.id);
            bindings.add(root);
            Map<String,Map<String,Object>> layout=new HashMap<>();
            for(Map.Entry<String,Map<String,Object>> entry:views.entrySet()){
                if(!bindings.contains(entry.getKey())){
                    layout.put(entry.getKey(),Collections.emptyMap());
                    continue;
                }
                Map<String,Object> properties=entry.getValue();
                if(!properties.containsKey("layout:left")||!properties.containsKey("layout:top")
                        ||!properties.containsKey("layout:width")||!properties.containsKey("layout:height"))
                    throw new IOException("encoded geometry unavailable");
                Map<String,Object> geometry=new HashMap<>();
                for(Map.Entry<String,Object> property:properties.entrySet()){
                    String key=property.getKey();
                    if(key.startsWith("layout:")||key.startsWith("scrolling:")||key.contains("visibility")
                            ||key.contains("translation")||key.contains("rotation")||key.contains("scale"))
                        geometry.put(key,property.getValue());
                }
                layout.put(entry.getKey(),geometry);
            }
            if(!layout.keySet().containsAll(bindings))throw new IOException("encoded bindings missing");
            return layout;
        }

        synchronized boolean refresh(boolean monitorPass)throws Exception{
            long attemptAt=System.nanoTime();
            if(!target.token.isBinderAlive()||!top(target)||!owned(target))return false;
            android.view.Display display=displays.getDisplay(target.display);if(display==null)throw new IOException("display removed");
            android.util.DisplayMetrics metrics=new android.util.DisplayMetrics();display.getRealMetrics(metrics);policy.viewport(metrics);
            String currentViewport=metrics.widthPixels+":"+metrics.heightPixels+":"+metrics.densityDpi+":"+display.getRotation();
            String displayViewport=currentViewport;
            long checkedRevision=revision;
            Map<String,Map<String,Object>> layout=null;
            // A reflective DUMP can stall the app's animation on its UI thread.
            // Compare view identities and geometry when borrowing a TextureView window layer.
            if (monitorPass && ready && enabled.get() && !suspended && scene != null && plan != null
                    && "texture".equals(plan.kind)
                    && ("window-direct".equals(plan.strategy) || ("system-scene".equals(plan.strategy)
                        && "com.tencent.mm.plugin.scanner.ui.BaseScanUI".equals(target.activity)))) {
                try{
                    layout=captureLayout();
                    if(checkedRevision==monitoredRevision&&displayViewport.equals(monitoredViewport)
                            &&layout.equals(monitoredLayout))return true;
                }catch(Exception ignored){/* Recheck via the full hierarchy. */}
            }
            monitoredLayout=null;
            List<Node> nodes=tree(target.token);
            for(Node n:nodes)if(root!=null&&n.id.startsWith("com.android.internal.policy.DecorView@")&&!root.equals(n.id)){generationReplaced=true;ready=false;++revision;throw new IOException("root recreated");}
            boolean fresh=camera==null||nodes.stream().noneMatch(n->n.id.equals(camera.id));
            if(fresh){
                // Capability discovery has its own bounded IPC timeouts. Preserve the
                // caller's remaining mutation budget instead of restoring a deadline
                // that expired while those independent checks were running.
                long saved=commandDeadline.get(),discoveryAt=System.nanoTime();commandDeadline.set(0L);
                try{
                    Map<String,Map<String,Object>> extra=Collections.emptyMap();try{extra=NativeAlphaSnapshot.encoded(target.token);}catch(Exception e){System.out.println("NATIVE_ALPHA_SNAPSHOT encoded="+e.getClass().getSimpleName());}
                    String theme="";try{theme=new String(WindowProbe.command(target.token,"DUMP_THEME","",2*1024*1024,1200),StandardCharsets.UTF_8);}catch(Exception failure){System.out.println("NATIVE_ALPHA_THEME_FAILED "+failure.getClass().getSimpleName()+":"+failure.getMessage());}
                    for(Node n:nodes){Map<String,Object> props=extra.get(n.id);n.backgroundKnownNull=NativeAlphaBackgroundEvidence.isAbsent(n.property("bg_"),props);
                        if(n.id.startsWith("com.android.internal.policy.DecorView@")){n.sceneFeature=NativeAlphaScene.transitionTheme(theme);n.backgroundResource=NativeAlphaSnapshot.backgroundResource(FakeContext.get(),pkg,n,props,theme);n.resourceFromTheme=n.backgroundResource!=null&&NativeAlphaSnapshot.exportedResource(n.property("mBackgroundResource"))==null;}
                        boolean leaf=NativeAlphaPolicy.previewLeaf(nodes,n);if(!leaf||n.number("layout:getWidth()")<nodes.get(0).number("layout:getWidth()")/3||(long)n.number("layout:getWidth()")*n.number("layout:getHeight()")<(long)nodes.get(0).number("layout:getWidth()")*nodes.get(0).number("layout:getHeight()")/6)continue;
                        if(n.id.contains("TextureView"))n.previewKind="texture";
                        if(n.id.contains("TextureView"))continue;
                        try{Object order=NativeAlphaSnapshot.scalar(target.token,n.id,"getCompositionOrder");if(order instanceof Integer){n.surfaceOrder=(Integer)order;n.previewKind="surface";}}catch(Exception ignored){System.out.println("NATIVE_ALPHA_CAPABILITY view="+n.id+" compositionOrder=unavailable reason="+ignored.getMessage());if(!n.id.contains("SurfaceView"))try{if(NativeAlphaSnapshot.scalar(target.token,n.id,"isAvailable") instanceof Boolean)n.previewKind="texture";}catch(Exception unavailable){}}
                    }
                    try{
                        boolean setter=false;try{android.view.SurfaceView.class.getMethod("setCompositionOrder",int.class);setter=true;}catch(Exception absent){}
                        if(setter){List<NativeAlphaLayers.Layer> first=NativeAlphaLayers.decode(NativeAlphaLayers.snapshot());Thread.sleep(40);List<NativeAlphaLayers.Layer> second=NativeAlphaLayers.decode(NativeAlphaLayers.snapshot());
                            for(Node n:nodes)if(n.surfaceOrder==null&&NativeAlphaPolicy.previewLeaf(nodes,n)){NativeAlphaLayers.Layer a=NativeAlphaLayers.find(first,pkg+"/"+target.activity,uid,n.id),b=NativeAlphaLayers.find(second,pkg+"/"+target.activity,uid,n.id);if(a!=null&&b!=null&&a.id==b.id&&a.z==b.z&&a.parent==b.parent&&a.relative==b.relative){n.surfaceOrder=a.z;n.previewKind="surface";System.out.println("NATIVE_ALPHA_COMPOSITOR_ORDER view="+n.id+" layer="+a.id+" original="+a.z);}}
                        }
                    }catch(Exception failure){System.out.println("NATIVE_ALPHA_COMPOSITOR_ORDER unavailable="+failure.getMessage());}
                    snapshots.clear();for(Node n:nodes)snapshots.put(n.id,n);
                    Node observedRoot=nodes.get(0);System.out.println("NATIVE_ALPHA_ROLLBACK resource="+(observedRoot.backgroundResource!=null)+" resourceEvidence="+(observedRoot.resourceFromTheme?"theme-class-match":"exported-or-none")+" backgroundKnownNull="+observedRoot.backgroundKnownNull+" sceneFeature="+observedRoot.sceneFeature);
                }finally{commandDeadline.set(saved==0L?0L:saved+(System.nanoTime()-discoveryAt));}
            }else for(Node n:nodes){Node old=snapshots.get(n.id);if(old!=null){n.surfaceOrder=old.surfaceOrder;n.backgroundResource=old.backgroundResource;n.backgroundKnownNull=old.backgroundKnownNull;n.resourceFromTheme=old.resourceFromTheme;n.previewKind=old.previewKind;n.sceneFeature=old.sceneFeature;}}

            lastTreeAt=System.nanoTime();
            NativeAlphaPolicy.Plan next=policy.plan(target.activity,nodes,camera==null?null:camera.id,colours,rootUnknown||unknownRoots.contains(target.token));
            if(next!=null&&scene!=null){if(directSource)policy.promoteWindow(next);else policy.promoteScene(next);}
            if(next==null)throw new IOException(camera==null?"camera layout pending":"camera_preview_disappeared");
            if(camera!=null&&camera.id.equals(next.camera.id)&&camera.zalpha&&camera.originalSubLayer==null)next.restore="closeScanner";
            currentViewport+=":"+java.util.Arrays.toString(NativeAlphaPolicy.screenBounds(next.camera))+":"+next.root.number("layout:getLocationOnScreen_x()")+":"+next.root.number("layout:getLocationOnScreen_y()")+":"+next.camera.number("layout:getLocationOnScreen_x()")+":"+next.camera.number("layout:getLocationOnScreen_y()")+":"+next.camera.number("layout:getWidth()")+":"+next.camera.number("layout:getHeight()");
            if(root!=null&&!root.equals(next.root.id)){generationReplaced=true;ready=false;++revision;throw new IOException("root recreated");}
            boolean sameBindings=plan!=null&&plan.backgrounds.keySet().equals(next.backgrounds.keySet());
            if(sameBindings)for(String role:plan.backgrounds.keySet())if(!plan.backgrounds.get(role).id.equals(next.backgrounds.get(role).id)){sameBindings=false;break;}
            if(plan!=null&&sameBindings&&currentViewport.equals(viewportKey)&&plan.signature.equals(next.signature)&&camera.id.equals(next.camera.id)&&plan.steps.toString().equals(next.steps.toString())){
                if(layout!=null){
                    if(monitoredRevision!=checkedRevision)
                        System.out.println("NATIVE_ALPHA_LAYOUT encoded_monitor=true strategy=" + plan.strategy);
                    monitoredLayout=layout;monitoredViewport=displayViewport;monitoredRevision=checkedRevision;
                }
                return true;
            }
            if("closeScanner".equals(next.restore)&&restoreScope==null)try{restoreScope=new NativeAlphaScene(atm,target.token,target.display,uid,pkg,target.activity,stop);}catch(Exception error){throw new NativeAlphaPolicy.Rejected("restore_scope_unavailable",next);}
            ready=false;++revision;Camera previous=camera;restoreProperties();
            if(suspended&&plan!=null&&(!plan.signature.equals(next.signature)||!camera.id.equals(next.camera.id)))suspended=false;
            System.out.println("NATIVE_ALPHA_CONTEXT atNs="+System.nanoTime()+" display="+target.display+" viewport="+currentViewport+" rootChanged="+(root!=null&&!root.equals(next.root.id))+" restore="+next.restore);
            root=next.root.id;rootUnknown=next.unknownRoot;plan=next;
            plan.attemptAt=attemptAt;
            viewportKey=currentViewport;
            camera=new Camera(next.camera);camera.zalpha="surface".equals(next.kind);
            if(previous!=null&&!previous.id.equals(camera.id))windowAttempted=false;
            if(previous!=null&&previous.id.equals(camera.id)){camera.alpha=previous.alpha;camera.originalSubLayer=previous.originalSubLayer;camera.exactOrder=previous.exactOrder;}
            if(enabled.get()&&!suspended)apply();if(scene!=null){scene.rebind();sourceRetried=true;}ready=true;
            return true;
        }
        private void apply()throws Exception{
            if(plan==null)return;
            for(int i=0;i<plan.steps.length();i++){
                org.json.JSONObject step=plan.steps.getJSONObject(i);String role=step.getString("target"),op=step.getString("operation");
                if("clearBackground".equals(op)){
                    Node n=plan.backgrounds.get(role);if(n==null)throw new IOException("background role disappeared");
                    if(n.backgroundResource!=null)resources.putIfAbsent(n.id,n.backgroundResource);
                    Integer colour=n.background();if(colour!=null)colours.putIfAbsent(n.id,colour);
                    if(n==plan.root&&rootUnknown)unknownRoots.add(target.token);
                    if(n==plan.root)set(target.token,n.id,"setBackgroundResource",'I',android.R.color.transparent);
                    else set(target.token,n.id,"setBackgroundColor",'I',0);applied=true;
                }else if("surfaceOnTop".equals(op)){camera.modified=true;applied=true;set(target.token,camera.id,"setZOrderOnTop",'Z',true);}
                else if("setCameraAlpha".equals(op)){camera.modified=true;set(target.token,camera.id,"setAlpha",'F',0f);}
                else if(!"prepareSystemScene".equals(op)&&!"prepareWindowSource".equals(op))throw new IOException("unknown operation");
            }
        }
        synchronized boolean activateScene()throws Exception{
            if(scene!=null&&directSource){scene.close(false);scene=null;directSource=false;}
            if(scene!=null)return false;if(sceneAttempted)throw new IOException("scene_already_attempted");sceneAttempted=true;ready=false;++revision;
            String previousViewport = viewportKey;
            String previousCamera = camera.id;
            String previousRoot = root;
            long prior=commandDeadline.get();commandDeadline.set(0L);
            int priorTimeout=commandTimeout.get();
            try{scene=new NativeAlphaScene(atm,target.token,target.display,uid,pkg,target.activity,stop,"closeScanner".equals(plan.restore));policy.promoteScene(plan);scene.activate();
                // The scanner UI thread is rebuilding its preview after the transition.
                commandTimeout.set(750);
                commandDeadline.set(System.nanoTime()+1800000000L);
                boolean observed = refresh();
                apply();plan.firstReadyAt=0;ready=true;
                if ("com.tencent.mm.plugin.scanner.ui.BaseScanUI".equals(target.activity)) {
                    sceneSelectionGraceUntil = System.nanoTime() + 1000000000L;
                }
                return observed && "com.tencent.mm.plugin.scanner.ui.BaseScanUI".equals(target.activity)
                        && "texture".equals(plan.kind) && top(target) && owned(target)
                        && previousCamera.equals(camera.id) && previousRoot.equals(root)
                        && previousViewport.equals(viewportKey);
            }finally{commandDeadline.set(prior);commandTimeout.set(priorTimeout);}
        }
        synchronized boolean activateWindow()throws Exception{
            if(windowAttempted||scene!=null||!plan.windowEligible||"closeScanner".equals(plan.restore))return false;
            windowAttempted=true;ready=false;++revision;NativeAlphaScene borrowed=null;
            long prior=commandDeadline.get();commandDeadline.set(0L);
            try{
                borrowed=new NativeAlphaScene(atm,target.token,target.display,uid,pkg,target.activity,stop,false);borrowed.rebind();
                policy.promoteWindow(plan);scene=borrowed;directSource=true;borrowed=null;sourceRetried=false;plan.firstReadyAt=0;
                System.out.println("NATIVE_ALPHA_WINDOW_DIRECT helperApk=false sceneTransition=false");return true;
            }catch(Exception error){if("window-direct".equals(plan.strategy))policy.mirrorSource(plan);System.out.println("NATIVE_ALPHA_WINDOW_DIRECT unavailable="+error);return false;}
            finally{if(borrowed!=null)borrowed.close(false);commandDeadline.set(prior);ready=true;}
        }
        synchronized boolean recoverSource()throws Exception{
            if(scene==null||sourceRetried)return false;sourceRetried=true;ready=false;++revision;
            try{scene.rebind();}finally{ready=true;}return true;
        }
        synchronized void releaseRestorableSource(){
            // An exact/resource restore keeps the same page alive. Keep its suspended
            // session too, otherwise the next poll would launch the same failed scene again.
            if(scene!=null&&!"closeScanner".equals(plan.restore)){scene.close(false);scene=null;directSource=false;}
        }
        synchronized void enable(boolean on)throws Exception{
            long started=System.nanoTime();
            ready=false;++revision;
            if(on){suspended=false;long before=revision;if(started-lastTreeAt>250000000L)refresh();plan.attemptAt=started;if(revision==before)apply();}else restoreProperties();
            ready=true;
        }
        private void restoreProperties(){
            long prior=commandDeadline.get();commandDeadline.set(System.nanoTime()+700000000L);
            try{
                if(camera!=null&&camera.modified){try{
                    set(target.token,camera.id,"setAlpha",'F',camera.alpha);
                    if(camera.zalpha){
                        if(camera.exactOrder)set(target.token,camera.id,"setCompositionOrder",'I',camera.originalSubLayer);
                        else set(target.token,camera.id,"setZOrderOnTop",'Z',Integer.valueOf(1).equals(camera.originalSubLayer));
                        if(!camera.exactOrder&&Integer.valueOf(-1).equals(camera.originalSubLayer))set(target.token,camera.id,"setZOrderMediaOverlay",'Z',true);
                    }
                    camera.modified=false;
                }catch(Exception e){System.out.println("NATIVE_ALPHA_RESTORE camera="+e.getClass().getSimpleName());}}
                for(Map.Entry<String,Integer> r:resources.entrySet())try{set(target.token,r.getKey(),"setBackgroundResource",'I',r.getValue());}catch(Exception e){System.out.println("NATIVE_ALPHA_RESTORE resource="+e.getClass().getSimpleName());}
                for(Map.Entry<String,Integer> c:colours.entrySet())try{if(c.getKey().equals(root))set(target.token,c.getKey(),"setBackgroundResource",'I',0);set(target.token,c.getKey(),"setBackgroundColor",'I',c.getValue());}catch(Exception ignored){}
            }finally{commandDeadline.set(prior);}
        }
        @Override public void close(){close(true);}
        void close(boolean restoreByNavigation){
            ready=false;running.set(false);if(monitor!=null)monitor.interrupt();
            synchronized(this){restoreProperties();
                if(scene!=null){scene.close(!directSource&&!generationReplaced&&plan!=null&&"closeScanner".equals(plan.restore));scene=null;}
                else if(restoreScope!=null&&applied&&!generationReplaced){try{restoreScope.finishOriginal();}catch(Exception e){System.out.println("NATIVE_ALPHA_RESTORE scope="+e.getClass().getSimpleName());}}
                else if(restoreByNavigation&&applied&&!generationReplaced&&(rootUnknown||(plan!=null&&"closeScanner".equals(plan.restore))))try{
                    Target current=select();if(current!=null&&current.token.equals(target.token)){Process p=new ProcessBuilder("input","-d",Integer.toString(target.display),"keyevent","4").start();p.waitFor();System.out.println("NATIVE_ALPHA_RESTORE closed_scanner_for_original_drawable");}
                }catch(Exception e){System.out.println("NATIVE_ALPHA_RESTORE failed="+e.getClass().getSimpleName());}
                if(restoreScope!=null){restoreScope.close(false);restoreScope=null;}
                if(mirror!=null)try{Object tr=tx.getConstructor().newInstance();try{call(tr,"remove",new Class[]{sc},mirror);call(tr,"apply",new Class[]{});}finally{call(tr,"close",new Class[]{});call(mirror,"release",new Class[]{});mirror=null;}}catch(Exception ignored){}
            }
        }
        Object captureArgs()throws Exception{
            Point size=new Point();displays.getDisplay(target.display).getRealSize(size);
            if(size.x<=0||size.y<=0||(long)size.x*size.y>16*1024*1024)throw new IOException("display size limit");
            Object builder=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(sc).newInstance(scene==null?mirror:scene.source());
            call(builder,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,size.x,size.y));call(builder,"setChildrenOnly",new Class[]{boolean.class},false);
            return call(builder,"build",new Class[]{});
        }
    }
    private static final class Captured implements AutoCloseable {
        final Object capture;
        final HardwareBuffer buffer;
        final long startedAt, finishedAt;

        Captured(Object args)throws Exception{
            startedAt=System.nanoTime();
            capture=Class.forName("android.window.ScreenCapture").getMethod("captureLayers",argsClass).invoke(null,args);
            if(capture==null)throw new IOException("native capture returned null");
            finishedAt=System.nanoTime();
            buffer=(HardwareBuffer)call(capture,"getHardwareBuffer",new Class[]{});
        }

        NativeAlphaReadback.Frame read(NativeAlphaReadback readback)throws Exception{
            Bitmap hardware=(Bitmap)call(capture,"asBitmap",new Class[]{});
            try{return readback.read(hardware);}
            finally{hardware.recycle();}
        }

        @Override public void close(){buffer.close();}
    }
    private static final class Prefetch implements AutoCloseable {
        private final ExecutorService executor=Executors.newSingleThreadExecutor(r->new Thread(r,"native-alpha-capture"));
        private CompletableFuture<Captured> pending;
        Session owner;
        long revision;
        int control;

        void start(Session session,long expectedRevision,int expectedControl){
            Object args;
            try{args=session.captureArgs();}catch(Exception ignored){return;}
            owner=session;
            revision=expectedRevision;
            control=expectedControl;
            pending=CompletableFuture.supplyAsync(()->{
                try{return new Captured(args);}catch(Exception error){throw new CompletionException(error);}
            },executor);
        }

        Captured take(){
            if(pending==null)return null;
            try{return pending.join();}catch(CompletionException ignored){return null;}
            finally{pending=null;}
        }

        @Override public void close(){
            try(Captured unused=take()){}finally{executor.shutdown();}
        }
    }
    private static void header(DataOutputStream out,int type,int len,int sequence,int epoch,int w,int h,long timestamp,int crc,int flags)throws IOException{
        out.writeInt(type);out.writeInt(len);out.writeInt(sequence);out.writeInt(epoch);out.writeInt(w);out.writeInt(h);out.writeLong(timestamp);out.writeInt(crc);out.writeInt(flags);
    }
    // An otherwise ordinary heartbeat can report that the scanner kept the same
    // camera view, root, rotation and viewport through the covered transition.
    private static final int SCENE_GEOMETRY_UNCHANGED = 1;
    private static final int SCENE_REJECTED = 1;

    private static boolean tryCoveredScene(Session session, DataOutputStream out,
            int sequence, int epoch, AtomicInteger coveredEpoch, AtomicBoolean releaseRequested) throws Exception {
        header(out, 6, 0, sequence, epoch, 0, 0, System.nanoTime(), 0, 0);
        out.flush();
        long deadline = System.nanoTime() + 1500000000L;
        while (!stop.get() && enabled.get() && !releaseRequested.get()) {
            if (coveredEpoch.get() == epoch) {
                try {
                    if (session.activateScene()) {
                        header(out, 3, 0, sequence, epoch, 0, 0, System.nanoTime(), 0, SCENE_GEOMETRY_UNCHANGED);
                        out.flush();
                    }
                    return true;
                } catch (Exception error) {
                    policy.result(session.target.activity, session.plan, NativeAlphaPolicy.sceneFailureReason(error),
                            0, false, false, session.target.seenAt);
                    System.out.println("NATIVE_ALPHA_SCENE_FAILED " + error);
                    return false;
                }
            }
            if (System.nanoTime() >= deadline) break;
            Thread.sleep(10);
        }
        System.out.println("NATIVE_ALPHA_SCENE_CANCELLED cover_not_presented");
        return false;
    }

    public static void main(String[] args)throws Exception{
        if((args.length!=2&&args.length!=3)||!args[0].matches("[A-Za-z][A-Za-z0-9_.]+")||!args[1].matches("ez_native_alpha_[0-9]+"))throw new IllegalArgumentException("package and private socket required");
        if(android.os.Process.myUid()!=2000)throw new SecurityException("shell UID required");
        pkg=args[0];Looper.prepareMainLooper();Workarounds.apply();
        android.content.Context ctx=FakeContext.get();uid=ctx.getPackageManager().getApplicationInfo(pkg,0).uid;
        policy=new NativeAlphaPolicy(ctx,pkg,args.length==3?args[2]:null);
        Constructor<DisplayManager> constructor=DisplayManager.class.getDeclaredConstructor(android.content.Context.class);constructor.setAccessible(true);displays=constructor.newInstance(ctx);
        sc=Class.forName("android.view.SurfaceControl");tx=Class.forName("android.view.SurfaceControl$Transaction");argsClass=Class.forName("android.window.ScreenCapture$LayerCaptureArgs");
        wm=service("window","android.view.IWindowManager");atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
        listener=Class.forName("android.window.WindowInfosListenerForTest").getConstructor().newInstance();Consumer<List<?>> consumer=list->{windows.set(list);windowChanged.set(true);};
        call(listener,"addWindowInfosListener",new Class[]{Consumer.class},consumer);
        Map<IBinder,Long> retry=new HashMap<>();
        try(LocalServerSocket server=new LocalServerSocket(args[1]);LocalSocket socket=server.accept();DataOutputStream out=new DataOutputStream(new BufferedOutputStream(socket.getOutputStream(),65536))){
            out.writeUTF("EZ_NATIVE_ALPHA_RGBA_V2");out.writeUTF(pkg);out.flush();
            AtomicBoolean keyframe=new AtomicBoolean(true);
            AtomicBoolean xorPatches=new AtomicBoolean();
            AtomicBoolean zstdPatches=new AtomicBoolean();
            AtomicBoolean releaseRequested=new AtomicBoolean();
            AtomicInteger coveredEpoch = new AtomicInteger();
            Thread controls = new Thread(() -> {
                try {
                    DataInputStream in = new DataInputStream(socket.getInputStream());
                    int c;
                    while ((c = in.read()) >= 0 && c != 0) {
                        if (c == 6) { coveredEpoch.set(in.readInt()); continue; }
                        if (c == 7) { xorPatches.set(true); continue; }
                        if (c == 9) { zstdPatches.set(true); continue; }
                        if (c == 5) { enabled.set(false); armed.set(false); releaseRequested.set(true); continue; }
                        if (releaseRequested.get()) continue;
                        if (c == 4) { keyframe.set(true); continue; }
                        if (c >= 1 && c <= 3) {
                            boolean on = c == 1, warm = c != 2;
                            boolean changed = enabled.getAndSet(on) != on;
                            changed |= armed.getAndSet(warm) != warm;
                            if (changed) controlRevision.incrementAndGet();
                        }
                    }
                } catch (IOException ignored) {
                } finally { stop.set(true); }
            }, "native-alpha-control");
            controls.setDaemon(true);
            controls.start();
            Session session=null;boolean lastEnabled=false,supported=false,reported=false,updates=false,reportedUpdates=false;int epoch=0,sequence=0,good=0,lastControl=0,badFrames=0;double bestCoverage=0;long driftAt=0;long badAt=0,nextPoll=0,lastBeat=0,lastCrc=-1,frames=0,activeRevision=-1,attemptAt=0,firstGoodCrc=-1;
            NativeAlphaFrameCodec codec=new NativeAlphaFrameCodec();NativeAlphaReadback readback=new NativeAlphaReadback(false,true);CRC32 crc=new CRC32();long rateAt=System.nanoTime(),rateFrames=0,captureNs=0,readNs=0,encodeNs=0,sendNs=0;String streamRecorded="",rateBackend="";
            long lastSentAt=0,maxGapNs=0,maxPollNs=0,duplicates=0,prefetchFrames=0;
            try(Prefetch prefetch=new Prefetch()){
                while(!stop.get())try(Captured prefetched=prefetch.take()){
                    if(stop.get())break;
                    readback.releasePlane();
                    long now=System.nanoTime();
                    if(releaseRequested.get()){
                        if(session!=null){session.close();session=null;}
                        retry.clear();
                        supported=false;lastEnabled=false;lastCrc=-1;activeRevision=-1;
                        coveredEpoch.set(0);keyframe.set(true);
                        releaseRequested.set(false);
                        header(out,5,0,sequence,++epoch,0,0,now,0,0);
                        out.flush();
                        System.out.println("NATIVE_ALPHA_PAUSED restored=true");
                        continue;
                    }
                    if(windowChanged.getAndSet(false)||now>=nextPoll){
                        long pollAt=System.nanoTime();
                        Target target=select();nextPoll=now+200000000L;
                        maxPollNs=Math.max(maxPollNs,System.nanoTime()-pollAt);
                        if (session != null && target == null && session.failure == null
                                && now < session.sceneSelectionGraceUntil && session.target.token.isBinderAlive()
                                && top(session.target) && owned(session.target)) {
                            // The scene transition can briefly remove the window from the visible list.
                            // Wait for selection to recover before capturing or changing the session.
                            nextPoll = 0;
                            Thread.sleep(50);
                            continue;
                        }
                        if(session!=null&&(target==null||!target.token.equals(session.target.token)||session.failure!=null)){
                            System.out.println("NATIVE_ALPHA_REBIND atNs="+System.nanoTime()+" reason="+(session.failure!=null?session.failure.getClass().getSimpleName()+":"+session.failure.getMessage():target==null?"page_not_selected":"window_changed"));
                            session.close(false);session=null;header(out,2,0,sequence,++epoch,0,0,now,0,0);out.flush();supported=false;lastEnabled=false;lastCrc=-1;
                        }
                        if(session==null&&target!=null&&armed.get()&&now>=retry.getOrDefault(target.token,0L))try{
                            session=new Session(target);lastEnabled=enabled.get();lastControl=controlRevision.get();activeRevision=-1;
                        }catch(NativeAlphaPolicy.Rejected e){
                            boolean transientLayout=e.getMessage().equals("ambiguous_preview");
                            if(!transientLayout)policy.result(target.activity,e.plan,e.getMessage(),0,false,false,target.seenAt);
                            retry.put(target.token,now+(transientLayout?300000000L:3000000000L));
                            System.out.println("NATIVE_ALPHA_FALLBACK reason="+e.getMessage());
                            if(!transientLayout&&enabled.get()){header(out,4,0,sequence,epoch,0,0,now,0,0);out.flush();}
                        }catch(Exception e){long delay=now-target.seenAt<1500000000L?250000000L:3000000000L;retry.put(target.token,now+delay);System.out.println("NATIVE_ALPHA_BIND pending="+e.getClass().getSimpleName()+":"+e.getMessage());}
                        if(retry.size()>64)retry.clear();
                    }
                    boolean on=enabled.get();int control=controlRevision.get();
                    if(session!=null&&(on!=lastEnabled||control!=lastControl)){try{session.enable(on);}catch(Exception e){session.failure=e;}lastEnabled=on;lastControl=control;}
                    if(session!=null&&activeRevision!=session.revision){activeRevision=session.revision;lastSentAt=maxGapNs=maxPollNs=duplicates=prefetchFrames=0;supported=false;reported=false;reportedUpdates=false;good=0;updates=false;firstGoodCrc=-1;lastCrc=-1;codec.reset();readback.invalidate();streamRecorded="";rateBackend="";session.plan.stream=null;badAt=0;badFrames=0;driftAt=0;bestCoverage=0;attemptAt=System.nanoTime();header(out,2,0,sequence,++epoch,0,0,now,0,0);out.flush();}
                    if(session==null||!on||session.suspended||!session.ready){if(now-lastBeat>250000000L){header(out,3,0,sequence,epoch,0,0,now,0,0);out.flush();lastBeat=now;}Thread.sleep(12);continue;}
                    if("window-direct".equals(session.plan.strategy)&&!session.windowAttempted){if(session.activateWindow())continue;}
                    if("system-scene".equals(session.plan.strategy)&&!session.sceneAttempted){
                        if(!tryCoveredScene(session,out,sequence,++epoch,coveredEpoch,releaseRequested)){retry.put(session.target.token,Long.MAX_VALUE);session.close();session=null;header(out,4,0,sequence,++epoch,0,0,now,0,SCENE_REJECTED);out.flush();}continue;
                    }
                    long revision=session.revision;NativeAlphaPolicy.Plan framePlan=session.plan;NativeAlphaReadback.Frame frame;
                    boolean overlapped=prefetched!=null&&prefetch.owner==session&&prefetch.revision==revision&&prefetch.control==control;
                    long capturedAt,captureTime,readbackTime;
                    try(Captured synchronous=overlapped?null:new Captured(session.captureArgs())){
                        Captured source=overlapped?prefetched:synchronous;
                        capturedAt=source.startedAt;
                        captureTime=source.finishedAt-capturedAt;
                        long readAt=System.nanoTime();
                        frame=source.read(readback);
                        readbackTime=System.nanoTime()-readAt;
                    }catch(Exception error){try{if(session.recoverSource())continue;}catch(Exception ignored){}session.failure=error;Thread.sleep(20);continue;}
                    int w=frame.width,h=frame.height;
                    if(!session.ready||revision!=session.revision||framePlan!=session.plan||w!=framePlan.environment.optInt("width")||h!=framePlan.environment.optInt("height"))continue;
                    int sampled=0,nonzero=0,partial=0,min=255,max=0,step=supported?Math.max(1,w*h/65536):1;
                    for(int i=0;i<w*h;i+=step){int a=frame.alphaAt(i);sampled++;if(a>0)nonzero++;if(a<255)partial++;min=Math.min(min,a);max=Math.max(max,a);}
                    boolean spatial=NativeAlphaPolicy.spatialAlpha(min,max,partial,nonzero,sampled);
                    framePlan.cameraAlphaCoverage=NativeAlphaPolicy.cameraAlphaCoverage(framePlan,w,h,supported,frame::alphaAt);
                    // A sparse monitor may miss small UI details. Confirm any failure with every A8 pixel before recovery.
                    if(supported&&(!spatial||framePlan.cameraAlphaCoverage<0.6)){
                        nonzero=partial=0;min=255;max=0;for(int i=0;i<w*h;i++){int a=frame.alphaAt(i);if(a>0)nonzero++;if(a<255)partial++;min=Math.min(min,a);max=Math.max(max,a);}
                        spatial=NativeAlphaPolicy.spatialAlpha(min,max,partial,nonzero,w*h);framePlan.cameraAlphaCoverage=NativeAlphaPolicy.cameraAlphaCoverage(framePlan,w,h,false,frame::alphaAt);
                    }
                    boolean drift=false;
                    if(supported&&"surface".equals(framePlan.kind)&&framePlan.sceneEligible&&!session.sceneAttempted){
                        bestCoverage=Math.max(bestCoverage,framePlan.cameraAlphaCoverage);
                        if(bestCoverage-framePlan.cameraAlphaCoverage>0.08){if(driftAt==0)driftAt=System.nanoTime();drift=System.nanoTime()-driftAt>=250000000L;}else driftAt=0;
                        if(drift)System.out.println("NATIVE_ALPHA_DRIFT coverage="+framePlan.cameraAlphaCoverage+" best="+bestCoverage+" action=early_window_source");
                    }
                    boolean valid=frame.alphaExact&&spatial&&framePlan.cameraAlphaCoverage>=0.6&&!drift;
                    if(!valid&&supported){
                        long badNow=System.nanoTime();if(badAt==0){badAt=badNow;System.out.println("NATIVE_ALPHA_TRANSIENT coverage="+framePlan.cameraAlphaCoverage+" action=confirm_before_recovery");}
                        ++badFrames;if(badFrames<3||badNow-badAt<350000000L){Thread.sleep(12);continue;}
                        System.out.println("NATIVE_ALPHA_RECOVERY coverage="+framePlan.cameraAlphaCoverage+" durationMs="+(badNow-badAt)/1000000+" sceneEligible="+framePlan.sceneEligible);
                    }
                    if(valid){badAt=0;badFrames=0;}
                    if(!valid){good=0;if(!supported&&System.nanoTime()-attemptAt<650000000L){Thread.sleep(12);continue;}
                        System.out.println("NATIVE_ALPHA_VERIFY valid=false source="+framePlan.captureSource+" strategy="+framePlan.strategy+" min="+min+" max="+max+" partial="+partial+" samples="+sampled+" coverage="+framePlan.cameraAlphaCoverage);
                        if(session.activateWindow())continue;
                        if(framePlan.sceneEligible&&!session.sceneAttempted){
                            if(!tryCoveredScene(session,out,sequence,++epoch,coveredEpoch,releaseRequested)){retry.put(session.target.token,Long.MAX_VALUE);session.close();session=null;header(out,4,0,sequence,++epoch,0,0,now,0,SCENE_REJECTED);out.flush();}continue;
                        }
                        if(session.scene!=null)try{if(session.recoverSource())continue;}catch(Exception ignored){}
                        policy.result(session.target.activity,framePlan,spatial?"opaque_camera_region":"opaque_or_blank",0,false,false,session.target.seenAt);session.suspended=true;session.enable(false);session.releaseRestorableSource();if(session.scene!=null||session.restoreScope!=null){session.close();session=null;}header(out,4,0,sequence,epoch,0,0,now,0,0);out.flush();continue;}
                    if(!frame.backend.equals(rateBackend)){rateBackend=frame.backend;rateAt=capturedAt;rateFrames=captureNs=readNs=encodeNs=sendNs=0;}
                    if(framePlan.stream==null||!frame.backend.equals(framePlan.stream.optString("backend")))framePlan.stream=new org.json.JSONObject().put("protocol",2).put("backend",frame.backend).put("inputColour",frame.inputColour).put("outputColour","sRGB").put("alphaExact",frame.alphaExact).put("rgbaExact",frame.rgbaExact).put("maxRgbError",frame.maxRgbError).put("fallback",frame.fallback).put("measured",false);
                    session.sourceRetried=false;crc.reset();
                    if(frame.plane==null)crc.update(frame.rgba);
                    else NativeAlphaFrameCodec.updateRows(crc,frame.plane,frame.rowStride,w*4,0,h);
                    long checksum=crc.getValue();if(firstGoodCrc<0)firstGoodCrc=checksum;else if(firstGoodCrc!=checksum)updates=true;
                    if(!supported){if(++good<2){Thread.sleep(12);continue;}supported=true;framePlan.firstReadyAt=System.nanoTime();System.out.println("NATIVE_ALPHA_SUPPORTED size="+w+"x"+h+" decisionMs="+(framePlan.firstReadyAt-session.target.seenAt)/1000000L+" cache="+framePlan.cache);}
                    if(!reported||(!reportedUpdates&&updates)){
                        policy.result(session.target.activity,framePlan,null,Math.max(2,good),true,updates,session.target.seenAt);reported=true;reportedUpdates|=updates;
                    }
                    if(checksum==lastCrc&&!keyframe.get()){
                        ++duplicates;
                        readback.releasePlane();
                        if(now-lastBeat>250000000L){header(out,3,0,sequence,epoch,w,h,now,0,0);out.flush();lastBeat=now;}
                        Thread.sleep(8);
                        continue;
                    }
                    if(enabled.get()&&!stop.get()&&revision==session.revision&&control==controlRevision.get())
                        prefetch.start(session,revision,control);
                    long encodeAt=System.nanoTime();boolean full=keyframe.getAndSet(false);boolean directPlane=frame.plane!=null;
                    codec.setXorEnabled(xorPatches.get());
                    codec.setZstdEnabled(zstdPatches.get());
                    codec.prepare(frame.rgba,frame.plane,frame.rowStride,w,h,full);
                    readback.releasePlane();long encodeDone=System.nanoTime();
                    if(!enabled.get()||stop.get()||revision!=session.revision||control!=controlRevision.get()){codec.reset();continue;}
                    header(out,1,codec.packed.size(),++sequence,epoch,w,h,capturedAt,(int)checksum,codec.flags);codec.packed.writeTo(out);out.flush();codec.commit();lastCrc=checksum;lastBeat=now;
                    session.startLayoutMonitor();
                    long sentAt=System.nanoTime();++frames;++rateFrames;
                    if(lastSentAt!=0)maxGapNs=Math.max(maxGapNs,sentAt-lastSentAt);
                    lastSentAt=sentAt;
                    if(overlapped)++prefetchFrames;
                    captureNs+=captureTime;readNs+=captureTime+readbackTime;encodeNs+=encodeDone-encodeAt;sendNs+=sentAt-encodeDone;
                    if(frames==1||sentAt-rateAt>=2000000000L){
                        if(rateFrames>=3&&!frame.backend.equals(streamRecorded)){
                            framePlan.stream.put("measured",true).put("sampleFrames",rateFrames).put("sourceFps",rateFrames*1e9/(sentAt-rateAt)).put("readMs",readNs/1e6/rateFrames).put("encodeMs",encodeNs/1e6/rateFrames).put("sendMs",sendNs/1e6/rateFrames);
                            policy.result(session.target.activity,framePlan,null,Math.max(2,good),true,updates,session.target.seenAt);streamRecorded=frame.backend;
                            System.out.println("NATIVE_ALPHA_STREAM automatic=true "+framePlan.stream);
                        }
                        System.out.println("NATIVE_ALPHA_FRAME count="+frames+" fps="+String.format(java.util.Locale.ROOT,"%.1f",rateFrames*1e9/(sentAt-rateAt))+" readMs="+readNs/1e6/rateFrames+" captureMs="+captureNs/1e6/rateFrames+" readbackMs="+(readNs-captureNs)/1e6/rateFrames+" encodeMs="+encodeNs/1e6/rateFrames+" sendMs="+sendNs/1e6/rateFrames+" bytes="+codec.packed.size()+" changedRows="+codec.changedRows+" encoding="+codec.flags+" backend="+frame.backend+" directPlane="+directPlane+" prefetched="+overlapped+" prefetchFrames="+prefetchFrames+" duplicates="+duplicates+" maxGapMs="+maxGapNs/1e6+" maxPollMs="+maxPollNs/1e6+" size="+w+"x"+h);rateAt=sentAt;rateFrames=captureNs=readNs=encodeNs=sendNs=maxGapNs=maxPollNs=duplicates=prefetchFrames=0;}
                    long remaining=16000000L-(System.nanoTime()-now);if(remaining>0)Thread.sleep(remaining/1000000L,(int)(remaining%1000000L));
                }
            }finally{codec.close();readback.close();if(session!=null)session.close();}
        }finally{stop.set(true);call(listener,"removeWindowInfosListener",new Class[]{Consumer.class},consumer);}
        System.out.println("NATIVE_ALPHA_STOPPED restored=true");System.exit(0);
    }
    private NativeAlphaProbe(){}
}
