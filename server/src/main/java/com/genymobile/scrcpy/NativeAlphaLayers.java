package com.genymobile.scrcpy;
import java.io.*;import java.nio.charset.StandardCharsets;import java.util.*;import java.util.concurrent.*;
/** Read compositor state without invoking app getters. IDs/UIDs are live evidence, never cached recipes. */
final class NativeAlphaLayers {
 static final class Layer {int id,uid,z,parent,relative;boolean hasZ;String name="";}
 static final class Field {int number,wire;long value;byte[] bytes;}
 static List<Field> fields(byte[] b)throws IOException {
  List<Field> fs=new ArrayList<>();int[] at={0};while(at[0]<b.length){if(fs.size()>100000)throw new IOException("proto_field_limit");long tag=varint(b,at);
   Field f=new Field();f.number=(int)(tag>>>3);f.wire=(int)(tag&7);if(f.number==0)throw new IOException("proto_zero_field");
   if(f.wire==0)f.value=varint(b,at);else {int n;if(f.wire==1)n=8;else if(f.wire==5)n=4;else if(f.wire==2){long len=varint(b,at);if(len<0||len>8*1024*1024)throw new IOException("proto_length");n=(int)len;}else throw new IOException("proto_wire");if(n>b.length-at[0])throw new IOException("proto_truncated");f.bytes=Arrays.copyOfRange(b,at[0],at[0]+n);at[0]+=n;}fs.add(f);
  }return fs;
 }
 static long varint(byte[] b,int[] at)throws IOException {long v=0;for(int s=0;s<70;s+=7){if(at[0]>=b.length)throw new IOException("proto_varint");int c=b[at[0]++]&255;v|=(long)(c&127)<<s;if((c&128)==0)return v;}throw new IOException("proto_varint_limit");}
 static List<Layer> decode(byte[] data)throws IOException {
  if(data.length>8*1024*1024)throw new IOException("layer_snapshot_limit");List<Field> root=fields(data);boolean magic=false;for(Field f:root)if(f.number==1&&f.wire==1&&Arrays.equals(f.bytes,"LYRTRACE".getBytes(StandardCharsets.US_ASCII)))magic=true;
  List<Field> snapshots=new ArrayList<>();if(magic){for(Field f:root)if(f.number==2&&f.wire==2)snapshots.add(f);}else{
   for(Field packet:root)if(packet.number==1&&packet.wire==2)for(Field f:fields(packet.bytes))if(f.number==93&&f.wire==2)snapshots.add(f);
  }
  if(snapshots.isEmpty())throw new IOException("no_layer_snapshot");List<Layer> out=new ArrayList<>();
  for(Field entry:snapshots)for(Field layers:fields(entry.bytes))if(layers.number==3&&layers.wire==2)for(Field item:fields(layers.bytes))if(item.number==1&&item.wire==2){try{Layer l=new Layer();for(Field f:fields(item.bytes))switch(f.number){case 1:if(f.wire!=0)throw new IOException("layer_id_type");l.id=(int)f.value;break;case 2:if(f.wire!=2)throw new IOException("layer_name_type");l.name=new String(f.bytes,StandardCharsets.UTF_8);break;case 10:if(f.wire!=0)throw new IOException("layer_order_type");l.z=(int)f.value;l.hasZ=true;break;case 25:l.parent=(int)f.value;break;case 26:l.relative=(int)f.value;break;case 53:l.uid=(int)f.value;break;}if(out.size()>20000)throw new IOException("layer_count_limit");out.add(l);}catch(IOException malformed){/* Reject this layer; an unrelated vendor extension must not invalidate other identities. */}}return out;
 }
 static Layer find(List<Layer> all,String window,int uid,String view){
  int at=view.lastIndexOf('@');if(at<0)return null;String prefix=view.substring(at+1)+" SurfaceView["+window+"]#";Map<Integer,Layer> ids=new HashMap<>();for(Layer l:all)ids.put(l.id,l);Layer found=null;
  for(Layer l:all)if(l.uid==uid&&l.hasZ&&l.name.startsWith(prefix)&&l.z>=-1000&&l.z<=1000){Layer root=ids.get(l.relative);if(root==null||root.uid!=uid||!root.name.startsWith(window+"#"))continue;boolean child=false;Layer p=l;Set<Integer> seen=new HashSet<>();while(p!=null&&seen.add(p.id)){if(p.id==root.id){child=true;break;}p=ids.get(p.parent);}if(child){if(found!=null)return null;found=l;}}
  return found;
 }
 static byte[] snapshot()throws Exception {
  String config="buffers { size_kb: 4096 fill_policy: RING_BUFFER }\nduration_ms: 150\ndata_sources { config { name: \"android.surfaceflinger.layers\" surfaceflinger_layers_config { mode: MODE_DUMP trace_flags: TRACE_FLAG_VIRTUAL_DISPLAYS trace_flags: TRACE_FLAG_EXTRA } } }\n";
  try{byte[] b=command(config,"perfetto","--txt","-c","-","-o","-");decode(b);return b;}
  catch(Exception unavailable){System.out.println("NATIVE_ALPHA_LAYER_SNAPSHOT perfetto="+unavailable.getMessage()+" fallback=legacy-proto");return command("","dumpsys","SurfaceFlinger","--proto");}
 }
 private static byte[] command(String input,String...args)throws Exception {
  Process p=new ProcessBuilder(args).redirectError(new File("/dev/null")).start();
  ExecutorService pool=Executors.newSingleThreadExecutor(r->{Thread t=new Thread(r);t.setDaemon(true);return t;});Future<byte[]> read=pool.submit(()->p.getInputStream().readNBytes(8*1024*1024+1));
  try{
   try(OutputStream writer=p.getOutputStream()){writer.write(input.getBytes(StandardCharsets.UTF_8));}
   if(!p.waitFor(2500,TimeUnit.MILLISECONDS))throw new IOException("layer_snapshot_timeout");byte[] b=read.get(200,TimeUnit.MILLISECONDS);if(p.exitValue()!=0||b.length>8*1024*1024)throw new IOException("layer_snapshot_failed");return b;
  }finally{if(p.isAlive())p.destroyForcibly();read.cancel(true);pool.shutdownNow();}
 }
}
