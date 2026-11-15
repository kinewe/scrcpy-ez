package com.genymobile.scrcpy;
import java.io.*;
import org.junit.Test;
import static org.junit.Assert.*;
public class NativeAlphaSnapshotTest {
 @Test public void orderGetterRequiresActualTypedReturn()throws Exception {
  ByteArrayOutputStream bytes=new ByteArrayOutputStream();DataOutputStream out=new DataOutputStream(bytes);out.writeChar('I');out.writeInt(-2);assertEquals(Integer.valueOf(-2),NativeAlphaSnapshot.decodeScalar(bytes.toByteArray()));
  try{NativeAlphaSnapshot.decodeScalar(new byte[]{0,'V'});fail("void is not an order snapshot");}catch(IOException expected){}
 }
 @Test public void viewportToleranceIsBounded(){assertTrue(NativeAlphaScene.viewportCompatible(3200,1440,3202,1440));assertFalse(NativeAlphaScene.viewportCompatible(576,1280,1440,3200));}
 @Test public void noIndependentUiRejectsWholePageSurface(){NativeAlphaProbe.Node root=new NativeAlphaProbe.Node(),cam=new NativeAlphaProbe.Node();cam.parent=root;assertFalse(NativeAlphaPolicy.independentUi(java.util.Arrays.asList(root,cam),cam));}
}
