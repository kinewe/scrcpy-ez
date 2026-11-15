package com.genymobile.scrcpy;
import android.os.IBinder;
import android.content.Context;
import android.graphics.drawable.Drawable;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
/** Live, per-window rollback evidence. Resource IDs and values never enter portable recipes. */
final class NativeAlphaSnapshot {
 static Object scalar(IBinder window,String view,String method)throws Exception {
  String args=android.util.Base64.encodeToString(new byte[]{0,0,0,0},2);
  String reply=new String(WindowProbe.command(window,"INVOKE_METHOD",view+" "+method+" "+args,65536,250),StandardCharsets.UTF_8);
  String[] lines=reply.split("\n");if(lines.length<2||!"1".equals(lines[0].trim()))throw new IOException("getter_unavailable:"+(lines.length>1?lines[1].trim().substring(0,Math.min(160,lines[1].trim().length())):"empty_reply"));
  return decodeScalar(android.util.Base64.decode(lines[1].trim(),2));
 }
 static Object decodeScalar(byte[] bytes)throws IOException {
  DataInputStream in=new DataInputStream(new ByteArrayInputStream(bytes));char type=in.readChar();Object result;
  switch(type){case 'I':result=in.readInt();break;case 'Z':result=in.readBoolean();break;case 'F':result=in.readFloat();break;default:throw new IOException("unsupported_getter_type");}
  if(in.available()!=0)throw new IOException("getter_trailing_bytes");return result;
 }
 static Map<String,Map<String,Object>> encoded(IBinder window)throws Exception {
  return decodeEncoded(WindowProbe.command(window,"DUMP_ENCODED","",8*1024*1024,650));
 }
 static Map<String,Map<String,Object>> decodeEncoded(byte[] data)throws Exception {
  DataInputStream in=new DataInputStream(new ByteArrayInputStream(data));List<Object> values=new ArrayList<>();
  while(in.available()>0)values.add(value(in,0));Map<?,?> table=null;
  for(Object o:values)if(o instanceof Map&&((Map<?,?>)o).containsValue("meta:__name__"))table=(Map<?,?>)o;
  if(table==null)throw new IOException("encoded_properties_missing");Map<String,Map<String,Object>> result=new HashMap<>();for(Object o:values)if(o!=table)walk(o,table,result);return result;
 }
 static Object value(DataInputStream in,int depth)throws IOException {
  if(depth>96)throw new IOException("hierarchy_depth_limit");int type=in.readUnsignedByte();
  switch(type){case 'Z':return in.readBoolean();case 'B':return in.readByte();case 'S':return in.readShort();case 'I':return in.readInt();case 'J':return in.readLong();case 'F':return in.readFloat();case 'D':return in.readDouble();case 'R':int n=in.readUnsignedShort();byte[] b=new byte[n];in.readFully(b);return new String(b,StandardCharsets.UTF_8);
   case 'M':Map<Integer,Object> m=new LinkedHashMap<>();for(int count=0;count<4096;count++){Object key=value(in,depth+1);if(!(key instanceof Number))throw new IOException("invalid_property_key");int k=((Number)key).intValue();if(k==0)return m;m.put(k,value(in,depth+1));}throw new IOException("property_count_limit");
   default:throw new IOException("unknown_property_type");}
 }
 static void walk(Object o,Map<?,?> table,Map<String,Map<String,Object>> result){
  if(!(o instanceof Map))return;Map<String,Object> fields=new HashMap<>();for(Map.Entry<?,?> e:((Map<?,?>)o).entrySet()){Object k=table.get(e.getKey());if(k==null)k=table.get(((Number)e.getKey()).shortValue());if(k instanceof String)fields.put((String)k,e.getValue());}
  Object name=fields.get("meta:__name__"),hash=fields.get("meta:__hash__");if(name instanceof String&&hash instanceof Number&&fields.containsKey("layout:width"))result.put(name+"@"+Integer.toHexString(((Number)hash).intValue()),fields);
  for(Object child:((Map<?,?>)o).values())if(child instanceof Map)walk(child,table,result);
 }
 static Integer resource(String value){
  if(value==null)return null;String v=value.trim();try{if(v.matches("@(?:0x)?[0-9a-fA-F]{8}")){int id=(int)Long.parseLong(v.substring(v.startsWith("@0x")?3:1),16);return id!=0?id:null;}if(v.matches("(?:0x)?[0-9a-fA-F]{8}")){int id=(int)Long.parseLong(v.replace("0x",""),16);return id!=0?id:null;}}catch(NumberFormatException ignored){}return null;
 }
 static Integer exportedResource(String value){try{if(value!=null&&value.matches("[0-9]{1,10}")){long n=Long.parseLong(value);return n>0&&n<=0xffffffffL?(int)n:null;}}catch(Exception ignored){}return resource(value);}
 private static final Map<String,Map<String,Set<Integer>>> tables=new HashMap<>();
 static Integer resourcePath(Context app,Context shell,String pkg,String path,String actual)throws Exception {
  android.content.pm.ApplicationInfo info=app.getApplicationInfo();List<String> apks=new ArrayList<>();apks.add(info.sourceDir);if(info.splitSourceDirs!=null)apks.addAll(Arrays.asList(info.splitSourceDirs));apks.add(shell.getPackageManager().getApplicationInfo("android",0).sourceDir);Set<Integer> found=new LinkedHashSet<>();
  for(String apk:apks){Map<String,Set<Integer>> index; synchronized(tables){index=tables.get(apk);if(index==null){index=NativeAlphaResourceTable.read(apk);if(tables.size()>8)tables.clear();tables.put(apk,index);}}Set<Integer> ids=index.get(path);if(ids==null)continue;for(int id:ids)try{android.util.TypedValue v=new android.util.TypedValue();app.getResources().getValue(id,v,true);Drawable d=app.getResources().getDrawable(id,app.getTheme());if(v.string!=null&&path.contentEquals(v.string)&&d!=null&&actual.equals(d.getClass().getName()))found.add(id);}catch(Exception ignored){}}
  if(found.size()==1){int id=found.iterator().next();System.out.println("NATIVE_ALPHA_RESOURCE path_resolved=true");return id;}return null;
 }
 static Integer backgroundResource(Context context,String pkg,NativeAlphaProbe.Node root,Map<String,Object> fields,String theme){
  // Prefer an exported exact resource ID when present. The theme fallback is labelled separately.
  Integer id=exportedResource(root.property("mBackgroundResource"));if(id!=null)return id;
  String actual=fields==null?null:(String)fields.get("miui:backgroundDrawableName");if(actual==null){actual=root.property("bg_");if(actual!=null&&actual.contains("@"))actual=actual.substring(0,actual.indexOf('@'));}if(actual==null||!actual.matches("android\\.graphics\\.drawable\\.[A-Za-z0-9_$]+"))return null;
  String[] lines=theme.split("\r?\n");
  for(int i=0;i+1<lines.length;i++)if("android:attr/windowBackground".equals(lines[i].trim())){
   try{Context app=context.createPackageContext(pkg,0);id=resource(lines[i+1]);
    String value=lines[i+1].trim();if(id==null&&value.matches("[A-Za-z0-9_.]+:[A-Za-z0-9_]+/[A-Za-z0-9_.]+")){int resolved=app.getResources().getIdentifier(value,null,null);if(resolved!=0)id=resolved;}
    if(id==null&&value.matches("res/drawable[^/]*/[A-Za-z0-9_.]+")){String file=value.substring(value.lastIndexOf('/')+1).replaceFirst("(?:\\.9)?\\.[A-Za-z0-9]+$","");int resolved=app.getResources().getIdentifier(file,"drawable",pkg);if(resolved!=0)id=resolved;}
    if(id==null&&value.startsWith("res/")){id=resourcePath(app,context,pkg,value,actual);}
    if(id==null){System.out.println("NATIVE_ALPHA_RESOURCE unresolved_theme_value="+value);return null;}Drawable d=app.getResources().getDrawable(id,app.getTheme());if(d!=null&&d.getClass().getName().equals(actual))return id;}catch(Exception ignored){}return null;
  }return null;
 }
}
