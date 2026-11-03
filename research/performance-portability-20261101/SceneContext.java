package com.genymobile.scrcpy;

import android.content.Context;
import android.graphics.*;
import android.hardware.HardwareBuffer;
import android.os.*;
import java.io.*;
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.Consumer;
import org.json.*;

/** Owned mirror and read-only target metadata, shared by scope experiments. */
final class SceneContext implements AutoCloseable {
 static final Class<?>[] NONE=new Class<?>[0];
 static Object call(Object o,String n,Class<?>[] t,Object...a)throws Exception{return o.getClass().getMethod(n,t).invoke(o,a);}
 static void put(String n,Object v)throws Exception{Field f=NativeAlphaProbe.class.getDeclaredField(n);f.setAccessible(true);f.set(null,v);}
 static Object get(Object o,String n)throws Exception{Field f=o.getClass().getDeclaredField(n);f.setAccessible(true);return f.get(o);}
 final String pkg,activity;final int uid,display,width,height;final IBinder window;
 final List<NativeAlphaProbe.Node> nodes;final List<NativeAlphaLayers.Layer> layers;
 final Object listener;final Consumer<List<?>> consumer;Object mirror;
 final Class<?> sc=Class.forName("android.view.SurfaceControl");
 static List<NativeAlphaProbe.Node> tree(IBinder token)throws Exception{
  String text=new String(WindowProbe.command(token,"DUMP","",8*1024*1024,1800),StandardCharsets.UTF_8);
  List<NativeAlphaProbe.Node> out=new ArrayList<>(),stack=new ArrayList<>();
  for(String line:text.split("\n")){String trim=line.stripLeading(),id=trim.split(" ",2)[0];if(!id.matches("[A-Za-z0-9_.$]+@[0-9a-fA-F]+"))continue;NativeAlphaProbe.Node n=new NativeAlphaProbe.Node();n.id=id;n.line=line;n.depth=line.length()-trim.length();while(!stack.isEmpty()&&stack.get(stack.size()-1).depth>=n.depth)stack.remove(stack.size()-1);n.parent=stack.isEmpty()?null:stack.get(stack.size()-1);stack.add(n);out.add(n);}
  if(out.isEmpty())throw new IOException("empty view hierarchy");return out;
 }
 SceneContext(String pkg)throws Exception{this(pkg,true);}
 @SuppressWarnings("unchecked") SceneContext(String pkg,boolean validateLayerStability)throws Exception{
  this.pkg=pkg;Context ctx=FakeContext.get();uid=ctx.getPackageManager().getApplicationInfo(pkg,0).uid;
  Object atm=Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);put("pkg",pkg);put("uid",uid);put("atm",atm);
  listener=Class.forName("android.window.WindowInfosListenerForTest").getConstructor().newInstance();put("listener",listener);
  Field wf=NativeAlphaProbe.class.getDeclaredField("windows");wf.setAccessible(true);AtomicReference<List<?>> windows=(AtomicReference<List<?>>)wf.get(null);CountDownLatch ready=new CountDownLatch(1);
  consumer=list->{windows.set(list);if(!list.isEmpty())ready.countDown();};call(listener,"addWindowInfosListener",new Class[]{Consumer.class},consumer);ready.await(2,TimeUnit.SECONDS);
  try{
   Method select=NativeAlphaProbe.class.getDeclaredMethod("select");select.setAccessible(true);Object target=select.invoke(null);if(target==null)throw new IOException("no unique owned target window");
   window=(IBinder)get(target,"token");display=(Integer)get(target,"display");activity=(String)get(target,"activity");
   nodes=tree(window);width=nodes.get(0).number("layout:getWidth()");height=nodes.get(0).number("layout:getHeight()");
   List<NativeAlphaLayers.Layer> first=NativeAlphaLayers.decode(NativeAlphaLayers.snapshot());Thread.sleep(40);layers=NativeAlphaLayers.decode(NativeAlphaLayers.snapshot());
   Map<Integer,NativeAlphaLayers.Layer> initial=new HashMap<>();for(NativeAlphaLayers.Layer l:first)initial.put(l.id,l);
   if(validateLayerStability)for(NativeAlphaLayers.Layer l:layers)if(l.uid==uid){NativeAlphaLayers.Layer old=initial.get(l.id);if(old==null||old.parent!=l.parent||old.z!=l.z)throw new IOException("layer graph changed during inventory");}
  }catch(Exception e){call(listener,"removeWindowInfosListener",new Class[]{Consumer.class},consumer);throw e;}
 }
 JSONObject metadata()throws Exception{
  android.content.pm.PackageInfo p=FakeContext.get().getPackageManager().getPackageInfo(pkg,0);
  JSONObject result=new JSONObject().put("package",pkg).put("versionName",p.versionName).put("versionCode",p.getLongVersionCode()).put("sdk",Build.VERSION.SDK_INT).put("model",Build.MODEL).put("fingerprint",Build.FINGERPRINT).put("uid",uid).put("display",display).put("activity",activity).put("width",width).put("height",height);
  JSONArray views=new JSONArray(),graph=new JSONArray();
  for(NativeAlphaProbe.Node n:nodes)if(n.id.matches("(?i).*(surface|texture|flutter|unity|glview|camera|preview|webview).*")||(n.number("layout:getWidth()")>=width*3/4&&n.number("layout:getHeight()")>=height*3/4)){
   views.put(new JSONObject().put("id",n.id).put("parent",n.parent==null?JSONObject.NULL:n.parent.id).put("width",n.number("layout:getWidth()")).put("height",n.number("layout:getHeight()")).put("visibility",n.property("getVisibility()")).put("alpha",n.property("drawing:getAlpha()")));
  }
  for(NativeAlphaLayers.Layer l:layers)if(l.uid==uid)graph.put(new JSONObject().put("id",l.id).put("parent",l.parent).put("relative",l.relative).put("z",l.z).put("name",l.name));
  return result.put("viewCount",nodes.size()).put("renderViews",views).put("layers",graph);
 }
 List<NativeAlphaLayers.Layer> candidates(){
  List<NativeAlphaLayers.Layer> result=new ArrayList<>();
  for(NativeAlphaProbe.Node n:nodes)if(n.number("layout:getWidth()")>width/3&&(long)n.number("layout:getWidth()")*n.number("layout:getHeight()")>(long)width*height/8){NativeAlphaLayers.Layer l=NativeAlphaLayers.find(layers,pkg+"/"+activity,uid,n.id);if(l!=null)result.add(l);}
  return result;
 }
 String[] subtree(NativeAlphaLayers.Layer root)throws Exception{
  Set<Integer> ids=new HashSet<>();ids.add(root.id);boolean more=true;while(more){more=false;for(NativeAlphaLayers.Layer l:layers)if(ids.contains(l.parent)&&ids.add(l.id))more=true;}
  List<String> names=new ArrayList<>();for(NativeAlphaLayers.Layer l:layers)if(ids.contains(l.id)){if(l.uid!=uid)throw new IOException("foreign descendant");names.add(l.name);}return names.toArray(new String[0]);
 }
 Object args(String[] names)throws Exception{
  if(mirror==null){Object wm=Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null);mirror=sc.getConstructor().newInstance();if(!Boolean.TRUE.equals(call(wm,"mirrorDisplay",new Class[]{int.class,sc},display,mirror)))throw new IOException("mirrorDisplay rejected");Object tx=Class.forName("android.view.SurfaceControl$Transaction").getConstructor().newInstance();try{call(tx,"setLayerStack",new Class[]{sc,int.class},mirror,2147483632);call(tx,"show",new Class[]{sc},mirror);call(tx,"apply",NONE);}finally{call(tx,"close",NONE);}Thread.sleep(150);}
  Object b=Class.forName("android.window.ScreenCapture$LayerCaptureArgs$Builder").getConstructor(sc).newInstance(mirror);call(b,"setSourceCrop",new Class[]{Rect.class},new Rect(0,0,width,height));call(b,"setChildrenOnly",new Class[]{boolean.class},false);call(b,"setUid",new Class[]{long.class},(long)uid);
  if(names!=null){call(b,"setCaptureMode",new Class[]{int.class},1);call(b,"setExcludeOrIncludeLayerNames",new Class[]{String[].class},(Object)names);}return call(b,"build",NONE);
 }
 Object capture(Object args)throws Exception{return Class.forName("android.window.ScreenCapture").getMethod("captureLayers",args.getClass()).invoke(null,args);}
 static JSONObject alpha(byte[] rgba,int w,int h)throws Exception{
  long zero=0,partial=0,opaque=0,centerPartial=0,centerTotal=0;int min=255,max=0;java.util.zip.CRC32 crc=new java.util.zip.CRC32();
  for(int y=0;y<h;y++)for(int x=0;x<w;x++){int a=rgba[(y*w+x)*4+3]&255;crc.update(a);min=Math.min(min,a);max=Math.max(max,a);if(a==0)zero++;else if(a==255)opaque++;else partial++;if(x>=w/4&&x<w*3/4&&y>=h/4&&y<h*3/4){centerTotal++;if(a<255)centerPartial++;}}
  return new JSONObject().put("min",min).put("max",max).put("zero",zero).put("partial",partial).put("opaque",opaque).put("centerPartial",centerPartial).put("centerTotal",centerTotal).put("crc",crc.getValue());
 }
 JSONObject measure(String label,String[] names,NativeAlphaReadback readback)throws Exception{
  Object args=args(names);JSONObject report=new JSONObject();
  for(int i=0;i<3;i++){long at=System.nanoTime();Object captured=capture(args);if(captured==null)throw new IOException("capture returned null");try{Bitmap hw=(Bitmap)call(captured,"asBitmap",NONE);try{NativeAlphaReadback.Frame frame=readback.read(hw);long elapsed=System.nanoTime()-at;if(i==2)report=alpha(frame.rgba,width,height).put("alphaExact",frame.alphaExact).put("backend",frame.backend).put("captureReadMs",elapsed/1e6);}finally{hw.recycle();}}finally{((HardwareBuffer)call(captured,"getHardwareBuffer",NONE)).close();}}
  return report.put("label",label).put("excludedNames",names==null?new JSONArray():new JSONArray(Arrays.asList(names)));
 }
 public void close(){
  if(mirror!=null)try{Object tx=Class.forName("android.view.SurfaceControl$Transaction").getConstructor().newInstance();try{call(tx,"remove",new Class[]{sc},mirror);call(tx,"apply",NONE);}finally{call(tx,"close",NONE);}call(mirror,"release",NONE);}catch(Exception ignored){}
  try{call(listener,"removeWindowInfosListener",new Class[]{Consumer.class},consumer);}catch(Exception ignored){}
 }
}
