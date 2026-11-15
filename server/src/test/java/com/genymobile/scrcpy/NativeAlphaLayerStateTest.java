package com.genymobile.scrcpy;
import java.util.*;import org.junit.Test;import static org.junit.Assert.*;
public class NativeAlphaLayerStateTest {
 private static byte[] field(int id,long value){java.io.ByteArrayOutputStream b=new java.io.ByteArrayOutputStream();varint(b,(long)id<<3);varint(b,value);return b.toByteArray();}
 private static void varint(java.io.ByteArrayOutputStream b,long v){do{int n=(int)(v&127);v>>>=7;b.write(v==0?n:n|128);}while(v!=0);}
 private static byte[] message(int id,byte[] value){java.io.ByteArrayOutputStream b=new java.io.ByteArrayOutputStream();varint(b,((long)id<<3)|2);varint(b,value.length);b.write(value,0,value.length);return b.toByteArray();}
 private static byte[] join(byte[]...values){java.io.ByteArrayOutputStream b=new java.io.ByteArrayOutputStream();for(byte[] v:values)b.write(v,0,v.length);return b.toByteArray();}
 @Test public void perfettoSnapshotEnvelopePreservesNegativeOrder()throws Exception {
  byte[] layer=join(field(1,11),message(2,"abc SurfaceView[org.example/org.example.Scan]#11".getBytes(java.nio.charset.StandardCharsets.UTF_8)),field(10,-2),field(25,10),field(26,10),field(53,10360));
  byte[] snapshot=message(3,message(1,layer));
  // Trace -> TracePacket -> surfaceflinger_layers_snapshot; unrelated packets are allowed.
  byte[] trace=join(message(1,field(8,123)),message(1,message(93,snapshot)));
  List<NativeAlphaLayers.Layer> decoded=NativeAlphaLayers.decode(trace);assertEquals(1,decoded.size());assertEquals(-2,decoded.get(0).z);assertTrue(decoded.get(0).hasZ);assertEquals(10360,decoded.get(0).uid);
  try{NativeAlphaLayers.decode(message(1,field(8,123)));fail("trace without layer snapshot accepted");}catch(java.io.IOException expected){}
  try{NativeAlphaLayers.decode(Arrays.copyOf(trace,trace.length-1));fail("truncated snapshot accepted");}catch(java.io.IOException expected){}
 }
 @Test public void compositionOrderNeedsOwnedWindowAncestry(){
  NativeAlphaLayers.Layer root=new NativeAlphaLayers.Layer(),sv=new NativeAlphaLayers.Layer();root.id=10;root.uid=10360;root.name="org.example/org.example.Scan#10";sv.id=11;sv.uid=10360;sv.z=-2;sv.hasZ=true;sv.relative=10;sv.parent=10;sv.name="abc SurfaceView[org.example/org.example.Scan]#11";List<NativeAlphaLayers.Layer> ls=Arrays.asList(root,sv);
  assertSame(sv,NativeAlphaLayers.find(ls,"org.example/org.example.Scan",10360,"android.view.SurfaceView@abc"));
  sv.uid=1000;assertNull(NativeAlphaLayers.find(ls,"org.example/org.example.Scan",10360,"android.view.SurfaceView@abc"));sv.uid=10360;sv.parent=99;assertNull(NativeAlphaLayers.find(ls,"org.example/org.example.Scan",10360,"android.view.SurfaceView@abc"));
 }
 @Test public void truncatedResourceTableIsRejected(){try{new NativeAlphaResourceTable(new byte[]{2,0,12,0}).paths();fail("truncated resource table");}catch(java.io.IOException expected){}}
}
