package com.genymobile.scrcpy;

import android.app.ActivityOptions;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.graphics.Rect;
import android.os.*;
import org.json.JSONObject;
import java.io.*;
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;

/** The bounded system-activity scene route; no installed helper or injected code. */
final class NativeAlphaScene implements AutoCloseable {
    static final String COMPONENT="com.android.settings/.SubSettings";
    static final String FRAGMENT="com.android.settings.DisplaySettings";
    private static final Class<?>[] NONE=new Class<?>[0];
    private final Object atm,controller,taskToken;
    private final IBinder window,scanner;
    private final int taskId,display,uid,systemUid;
    private final String pkg,activity;
    private final Rect bounds;
    private IBinder bridge;
    private Object source;
    private boolean converted,closed;
    private final AtomicBoolean stopping;
    long transitionMs;
    private static Object call(Object o,String name,Class<?>[] types,Object...args)throws Exception{return o.getClass().getMethod(name,types).invoke(o,args);}
    static boolean windowSourceReady(){try{
        Class.forName("android.window.IWindowOrganizerController").getMethod("applySyncTransaction",Class.forName("android.window.WindowContainerTransaction"),Class.forName("android.window.IWindowContainerTransactionCallback"));
        Class.forName("android.app.IActivityClientController").getMethod("getTaskForActivity",IBinder.class,boolean.class);
        Class.forName("android.view.SurfaceControl").getMethod("getLayerId");Parcel.class.getMethod("hasBinders",int.class,int.class);Parcel.class.getMethod("readString8");return true;
    }catch(ReflectiveOperationException|RuntimeException unavailable){return false;}}
    static boolean frameworkReady(){try{
        Class<?> info=Class.forName("android.app.ActivityOptions$SceneTransitionInfo");info.getConstructor();
        info.getMethod("setReturning",boolean.class);info.getMethod("setSharedElementNames",ArrayList.class);info.getMethod("setResultReceiver",ResultReceiver.class);info.getMethod("setExitCoordinatorKey",int.class);
        ActivityOptions.class.getMethod("setSceneTransitionInfo",info);Field animation=ActivityOptions.class.getDeclaredField("mAnimationType");animation.setAccessible(true);
        Class<?> controller=Class.forName("android.app.IActivityClientController");controller.getMethod("convertToTranslucent",IBinder.class,Bundle.class);controller.getMethod("finishActivity",IBinder.class,int.class,Intent.class,int.class);
        Class.forName("android.window.IWindowOrganizerController").getMethod("applySyncTransaction",Class.forName("android.window.WindowContainerTransaction"),Class.forName("android.window.IWindowContainerTransactionCallback"));
        Class.forName("android.view.SurfaceControl").getMethod("getLayerId");Parcel.class.getMethod("hasBinders",int.class,int.class);Parcel.class.getMethod("readString8");return true;
    }catch(ReflectiveOperationException|RuntimeException e){return false;}}
    static JSONObject support(Context ctx)throws Exception {
        if(!frameworkReady())throw new IOException("scene_framework_unavailable");
        PackageManager pm=ctx.getPackageManager();ActivityInfo a=pm.getActivityInfo(ComponentName.unflattenFromString(COMPONENT),0);
        if(!a.exported||!a.enabled||!a.applicationInfo.enabled||a.applicationInfo.uid!=1000||(a.applicationInfo.flags&android.content.pm.ApplicationInfo.FLAG_SYSTEM)==0)throw new IOException("system_bridge_unavailable");
        PackageInfo p=pm.getPackageInfo(a.packageName,PackageManager.GET_SIGNING_CERTIFICATES);List<String> hashes=new ArrayList<>();
        for(Signature signature:p.signingInfo.getApkContentsSigners()){byte[] hash=MessageDigest.getInstance("SHA-256").digest(signature.toByteArray());StringBuilder s=new StringBuilder();for(byte b:hash)s.append(String.format(Locale.ROOT,"%02x",b&255));hashes.add(s.toString());}Collections.sort(hashes);
        return new JSONObject().put("component",COMPONENT).put("fragment",FRAGMENT).put("versionName",p.versionName).put("versionCode",p.getLongVersionCode()).put("lastUpdateTime",p.lastUpdateTime).put("signingSha256",String.join(",",hashes));
    }
    static boolean sameRoute(JSONObject a,JSONObject b){return a!=null&&b!=null&&COMPONENT.equals(a.optString("component"))&&COMPONENT.equals(b.optString("component"))&&FRAGMENT.equals(a.optString("fragment"))&&FRAGMENT.equals(b.optString("fragment"));}
    static boolean sameSupport(JSONObject a,JSONObject b){if(a==null||b==null)return false;for(String k:new String[]{"component","fragment","versionName","versionCode","lastUpdateTime","signingSha256"})if(!String.valueOf(a.opt(k)).equals(String.valueOf(b.opt(k))))return false;return true;}
    static boolean eligiblePreview(String kind,boolean bridgeReady,int width,int height,int displayWidth,int displayHeight){
        return bridgeReady&&("texture".equals(kind)||"surface".equals(kind))&&width>0&&height>0&&viewportCompatible(width,height,displayWidth,displayHeight);
    }
    static boolean viewportCompatible(int width,int height,int displayWidth,int displayHeight){return width>0&&height>0&&Math.abs(width-displayWidth)<=16&&Math.abs(height-displayHeight)<=16&&Math.abs(width-displayWidth)<=displayWidth/100.0&&Math.abs(height-displayHeight)<=displayHeight/100.0;}
    static boolean surfaceReady(){try{android.view.SurfaceView.class.getMethod("setZOrderOnTop",boolean.class);android.view.View.class.getMethod("setAlpha",float.class);return true;}catch(ReflectiveOperationException e){return false;}}
    static int transitionTheme(String theme){String[] lines=theme.split("\\r?\\n");for(int i=0;i+1<lines.length;i++)if("android:attr/windowActivityTransitions".equals(lines[i].trim())){String value=lines[i+1].trim();if("true".equalsIgnoreCase(value))return 1;if("false".equalsIgnoreCase(value))return 0;}return -1;}
    NativeAlphaScene(Object atm,IBinder window,int display,int uid,String pkg,String activity,AtomicBoolean stopping)throws Exception {this(atm,window,display,uid,pkg,activity,stopping,true);}
    NativeAlphaScene(Object atm,IBinder window,int display,int uid,String pkg,String activity,AtomicBoolean stopping,boolean requireChild)throws Exception {
        this.atm=atm;this.window=window;this.display=display;this.uid=uid;this.systemUid=(uid/100000)*100000+1000;this.pkg=pkg;this.activity=activity;this.stopping=stopping;
        controller=call(atm,"getActivityClientController",NONE);Object task=null;
        for(Object t:(List<?>)call(atm,"getAllRootTaskInfos",NONE))if(t.getClass().getField("displayId").getInt(t)==display){ComponentName top=(ComponentName)t.getClass().getField("topActivity").get(t);if(top!=null&&top.getPackageName().equals(pkg)&&top.getClassName().equals(activity)){if(task!=null)throw new IOException("ambiguous_scene_task");task=t;}}
        if(task==null)throw new IOException("scene_task_missing");taskId=task.getClass().getField("taskId").getInt(task);taskToken=task.getClass().getField("token").get(task);
        Object config=task.getClass().getField("configuration").get(task),wc=config.getClass().getField("windowConfiguration").get(config);bounds=new Rect((Rect)call(wc,"getBounds",NONE));
        IBinder token=null;
        for(Object h:NativeAlphaProbe.rawWindows())if(isHandle(h,uid,pkg+"/"+activity)&&window.equals(call(h,"getWindowToken",NONE))){IBinder candidate=appToken(h);if(candidate!=null&&taskOf(candidate)==taskId){if(token!=null&&!token.equals(candidate))throw new IOException("ambiguous_scene_token");token=candidate;}}
        if(token==null)throw new IOException("scene_activity_token_missing");scanner=token;
        if(requireChild&&(Integer)call(controller,"getTaskForActivity",new Class[]{IBinder.class,boolean.class},scanner,true)==taskId)throw new IOException("no_child_activity_restore_scope");
    }
    private boolean isHandle(Object h,int owner,String name)throws Exception {Class<?> c=h.getClass();String n=(String)c.getField("name").get(h);return c.getField("displayId").getInt(h)==display&&c.getField("ownerUid").getInt(h)==owner&&n.contains(name)&&!n.contains("InputSink");}
    private static IBinder appToken(Object h)throws Exception {Object a=h.getClass().getField("inputApplicationHandle").get(h);return a==null?null:(IBinder)a.getClass().getField("token").get(a);}
    private int taskOf(IBinder token)throws Exception{return (Integer)call(controller,"getTaskForActivity",new Class[]{IBinder.class,boolean.class},token,false);}
    private void check()throws Exception {if(stopping.get()||closed||!window.isBinderAlive()||taskOf(scanner)!=taskId)throw new IOException("scene_cancelled");}
    private Set<IBinder> systemTokens()throws Exception {Set<IBinder> out=new HashSet<>();for(Object h:NativeAlphaProbe.rawWindows())if(isHandle(h,systemUid,"com.android.settings/")){IBinder t=appToken(h);if(t!=null)out.add(t);}return out;}
    private final class Exchange extends ResultReceiver {
        final CountDownLatch received=new CountDownLatch(1);volatile ResultReceiver remote;boolean completed;
        Exchange(){super(null);}
        @Override protected void onReceiveResult(int code,Bundle data){if(code==100&&Binder.getCallingUid()==uid&&data!=null){remote=data.getParcelable("android:remoteReceiver",ResultReceiver.class);if(remote!=null)received.countDown();}}
        ActivityOptions options()throws Exception {
            ActivityOptions options=ActivityOptions.makeBasic();Field f=ActivityOptions.class.getDeclaredField("mAnimationType");f.setAccessible(true);f.setInt(options,5);
            Class<?> type=Class.forName("android.app.ActivityOptions$SceneTransitionInfo");Object info=type.getConstructor().newInstance();
            call(info,"setReturning",new Class[]{boolean.class},false);call(info,"setSharedElementNames",new Class[]{ArrayList.class},new ArrayList<String>());call(info,"setResultReceiver",new Class[]{ResultReceiver.class},this);call(info,"setExitCoordinatorKey",new Class[]{int.class},-1);call(options,"setSceneTransitionInfo",new Class[]{type},info);return options;
        }
        synchronized void complete(){if(remote!=null&&!completed){completed=true;remote.send(103,new Bundle());remote.send(104,new Bundle());}}
    }
    private void finish(IBinder token)throws Exception {call(controller,"finishActivity",new Class[]{IBinder.class,int.class,Intent.class,int.class},token,0,null,0);}
    private static String shell(String...argv)throws Exception {
        return shellLimit(32768,argv);
    }
    private static String shellLimit(int cap,String...argv)throws Exception {
        java.lang.Process process=new ProcessBuilder(argv).redirectErrorStream(true).start();ExecutorService io=Executors.newSingleThreadExecutor(r->{Thread t=new Thread(r,"alpha-scene-command");t.setDaemon(true);return t;});
        Future<byte[]> read=io.submit(()->{try(InputStream in=process.getInputStream()){return in.readNBytes(cap+1);}});
        try{if(!process.waitFor(2200,TimeUnit.MILLISECONDS))throw new IOException("scene_command_timeout");byte[] bytes=read.get(300,TimeUnit.MILLISECONDS);if(bytes.length>cap||process.exitValue()!=0)throw new IOException("scene_command_failed");return new String(bytes,StandardCharsets.UTF_8);}
        finally{if(process.isAlive())process.destroyForcibly();read.cancel(true);io.shutdownNow();}
    }
    void activate()throws Exception {
        check();String theme="";try{theme=new String(WindowProbe.command(window,"DUMP_THEME","",512*1024,500),StandardCharsets.UTF_8);}catch(Exception ignored){}
        if(transitionTheme(theme)==0)throw new IOException("scene_window_feature_unavailable");
        Set<IBinder> before=systemTokens();Exchange exchange=new Exchange();long begin=System.nanoTime();
        try{
            converted=Boolean.TRUE.equals(call(controller,"convertToTranslucent",new Class[]{IBinder.class,Bundle.class},scanner,null));
            String reply=shell("am","start","-W","--user",Integer.toString(uid/100000),"--display",Integer.toString(display),"--task",Integer.toString(taskId),"--activity-no-animation","-n",COMPONENT,"--es",":settings:show_fragment",FRAGMENT,"--ez",":settings:show_fragment_as_subsetting","true");
            if(reply.contains("Error:")||reply.contains("Exception"))throw new IOException("system_bridge_launch_rejected");
            for(int i=0;i<8&&bridge==null;i++){check();for(Object h:NativeAlphaProbe.rawWindows())if(isHandle(h,systemUid,"com.android.settings/com.android.settings.SubSettings")){IBinder t=appToken(h);if(t!=null&&!before.contains(t)&&taskOf(t)==taskId){if(bridge!=null&&!bridge.equals(t))throw new IOException("ambiguous_system_bridge");bridge=t;}}if(bridge==null)Thread.sleep(50);}
            if(bridge==null)throw new IOException("system_bridge_wrong_task");
            call(controller,"convertToTranslucent",new Class[]{IBinder.class,Bundle.class},bridge,exchange.options().toBundle());finish(bridge);bridge=null;
            if(!exchange.received.await(1000,TimeUnit.MILLISECONDS)||exchange.remote==null)throw new IOException("scene_receiver_missing");check();exchange.complete();transitionMs=(System.nanoTime()-begin)/1000000L;
            System.out.println("NATIVE_ALPHA_SCENE converted=true transitionMs="+transitionMs+" task="+taskId+" display="+display+" helperApk=false");
            Thread.sleep(100);rebind();
        }finally{
            exchange.complete();
            // Only finish new known system pages, never a settings page already open before us.
            for(Object h:NativeAlphaProbe.rawWindows())try{if(isHandle(h,systemUid,"com.android.settings/com.android.settings.SubSettings")||isHandle(h,systemUid,"com.android.settings/com.android.settings.homepage.DeepLinkHomepageActivity")){IBinder t=appToken(h);if(t!=null&&!before.contains(t)&&taskOf(t)==taskId)finish(t);}}catch(Exception e){System.out.println("NATIVE_ALPHA_SCENE cleanup="+e.getClass().getSimpleName());}
            bridge=null;
        }
    }
    static Set<Integer> scopedLayerIds(String text,String windowName,int owner)throws IOException {
        Set<Integer> named=new HashSet<>(),owned=new HashSet<>();
        java.util.regex.Pattern full=java.util.regex.Pattern.compile("^\\s*Layer \\[(\\d+)\\] "+java.util.regex.Pattern.quote(windowName)+"#(\\d+)\\s*$");
        java.util.regex.Pattern identity=java.util.regex.Pattern.compile("#(\\d+)(?: parent=\\d+)? flags:\\S+ .*?\\buid=(\\d+)(?:\\D|$)");
        for(String line:text.split("\n")){
            java.util.regex.Matcher name=full.matcher(line);if(name.matches()&&name.group(1).equals(name.group(2)))named.add(Integer.parseInt(name.group(1)));
            java.util.regex.Matcher uidMatch=identity.matcher(line);if(uidMatch.find()&&Integer.parseInt(uidMatch.group(2))==owner)owned.add(Integer.parseInt(uidMatch.group(1)));
        }
        // SurfaceFlinger truncates long names in the ownership tree, but preserves their layer IDs.
        // Join the full-name inventory to the ownership tree; never accept a title alone.
        named.retainAll(owned);if(named.isEmpty())throw new IOException("scene_layer_identity_missing");return named;
    }
    private Set<Integer> layerIds()throws Exception {
        return scopedLayerIds(shellLimit(4*1024*1024,"dumpsys","SurfaceFlinger"),pkg+"/"+activity,uid);
    }
    private Map<IBinder,Integer> handles(Parcel parcel)throws Exception {
        if(parcel.dataSize()>512*1024)throw new IOException("scene_transaction_size");Map<IBinder,Integer> result=new HashMap<>();Method has=Parcel.class.getMethod("hasBinders",int.class,int.class);
        for(int off=0;off+24<=parcel.dataSize();off+=4)if((Boolean)has.invoke(parcel,off,24))try{parcel.setDataPosition(off);IBinder b=parcel.readStrongBinder();if(b!=null&&"android.Layer.LayerHandle".equals(b.getInterfaceDescriptor())&&parcel.dataAvail()>=4){int id=parcel.readInt();if(id>0)result.put(b,id);}}catch(RuntimeException ignored){}
        return result;
    }
    private static Object wrap(IBinder handle,int id)throws Exception {
        Class<?> sc=Class.forName("android.view.SurfaceControl");Object b=Class.forName("android.view.SurfaceControl$Builder").getConstructor().newInstance();call(b,"setName",new Class[]{String.class},"EZ scoped alpha handle template");call(b,"setContainerLayer",NONE);Object template=call(b,"build",NONE);
        Parcel original=Parcel.obtain(),out=Parcel.obtain();
        try{((Parcelable)template).writeToParcel(original,0);original.setDataPosition(0);Parcel.class.getMethod("readString8").invoke(original);original.readInt();original.readInt();original.readInt();original.readStrongBinder();int start=original.dataPosition();original.readStrongBinder();int end=original.dataPosition();if(original.dataSize()>4096||end+4>original.dataSize())throw new IOException("surface_parcel_layout");
            out.appendFrom(original,0,start);out.writeStrongBinder(handle);out.writeInt(id);out.appendFrom(original,end+4,original.dataSize()-end-4);out.setDataPosition(0);Object result=((Parcelable.Creator<?>)sc.getField("CREATOR").get(null)).createFromParcel(out);
            if((Integer)call(result,"getLayerId",NONE)!=id){call(result,"release",NONE);throw new IOException("surface_identity_mismatch");}return result;
        }finally{original.recycle();out.recycle();call(template,"release",NONE);}
    }
    synchronized void rebind()throws Exception {
        check();Class<?> wct=Class.forName("android.window.WindowContainerTransaction"),token=Class.forName("android.window.WindowContainerToken"),cb=Class.forName("android.window.IWindowContainerTransactionCallback"),tx=Class.forName("android.view.SurfaceControl$Transaction");
        Object organizer=call(atm,"getWindowOrganizerController",NONE);CountDownLatch ready=new CountDownLatch(1);Map<IBinder,Integer> found=new ConcurrentHashMap<>();List<Throwable> errors=new CopyOnWriteArrayList<>();
        Binder callback=new Binder(){
            {attachInterface(null,"android.window.IWindowContainerTransactionCallback");}
            @Override protected boolean onTransact(int code,Parcel data,Parcel reply,int flags)throws RemoteException {
                if(code!=IBinder.FIRST_CALL_TRANSACTION)return super.onTransact(code,data,reply,flags);Object transaction=null;Parcel p=Parcel.obtain();
                try{data.enforceInterface("android.window.IWindowContainerTransactionCallback");data.readInt();transaction=data.readTypedObject((Parcelable.Creator<?>)tx.getField("CREATOR").get(null));((Parcelable)transaction).writeToParcel(p,0);found.putAll(handles(p));}
                catch(Throwable e){errors.add(e);}finally{try{if(transaction!=null){call(transaction,"apply",NONE);call(transaction,"close",NONE);}}catch(Exception e){errors.add(e);}p.recycle();ready.countDown();}if(reply!=null)reply.writeNoException();return true;
            }
        };
        Object proxy=Class.forName("android.window.IWindowContainerTransactionCallback$Stub").getMethod("asInterface",IBinder.class).invoke(null,callback),change=wct.getConstructor().newInstance();Rect currentBounds=null;for(Object t:(List<?>)call(atm,"getAllRootTaskInfos",NONE))if(t.getClass().getField("taskId").getInt(t)==taskId&&t.getClass().getField("displayId").getInt(t)==display){Object c=t.getClass().getField("configuration").get(t),w=c.getClass().getField("windowConfiguration").get(c);currentBounds=new Rect((Rect)call(w,"getBounds",NONE));}
        if(currentBounds==null)throw new IOException("scene_task_recreated");call(change,"setBounds",new Class[]{token,Rect.class},taskToken,currentBounds);
        call(organizer,"applySyncTransaction",new Class[]{wct,cb},change,proxy);if(!ready.await(1200,TimeUnit.MILLISECONDS)||!errors.isEmpty())throw new IOException("scene_window_sync_failed");check();Set<Integer> allowed=layerIds();Object selected=null;
        try{for(Map.Entry<IBinder,Integer> item:found.entrySet())if(allowed.contains(item.getValue())){if(selected!=null)throw new IOException("ambiguous_scene_layer");selected=wrap(item.getKey(),item.getValue());}if(selected==null)throw new IOException("scene_window_layer_missing");if(source!=null)call(source,"release",NONE);source=selected;selected=null;
            System.out.println("NATIVE_ALPHA_WINDOW_SOURCE layer="+call(source,"getLayerId",NONE)+" task="+taskId+" display="+display);
        }finally{if(selected!=null)call(selected,"release",NONE);}
    }
    synchronized Object source()throws Exception {check();if(source==null)throw new IOException("scene_source_missing");return source;}
    private boolean originalWindowPresent()throws Exception {
        for(Object h:NativeAlphaProbe.rawWindows())if(isHandle(h,uid,pkg+"/"+activity)&&window.equals(call(h,"getWindowToken",NONE)))return true;
        return false;
    }
    synchronized void finishOriginal()throws Exception {
        if(taskOf(scanner)==taskId&&originalWindowPresent()){finish(scanner);System.out.println("NATIVE_ALPHA_RESTORE closed_original_child_activity task="+taskId);}
        else System.out.println("NATIVE_ALPHA_RESTORE skipped_replaced_window task="+taskId);
    }
    @Override public synchronized void close(){close(true);}
    synchronized void close(boolean finishWindow){if(closed)return;closed=true;
        try{if(bridge!=null)finish(bridge);}catch(Exception ignored){}
        if(converted)try{if(taskOf(scanner)==taskId){call(controller,"convertFromTranslucent",new Class[]{IBinder.class},scanner);System.out.println("NATIVE_ALPHA_SCENE restored="+(finishWindow?"original_scope":"opacity_only_replaced_root"));}}catch(Exception e){System.out.println("NATIVE_ALPHA_SCENE restore="+e.getClass().getSimpleName());}
        if(finishWindow)try{finishOriginal();}catch(Exception e){System.out.println("NATIVE_ALPHA_SCENE scope_restore="+e.getClass().getSimpleName());}
        if(source!=null)try{call(source,"release",NONE);}catch(Exception ignored){}source=null;
    }
}
