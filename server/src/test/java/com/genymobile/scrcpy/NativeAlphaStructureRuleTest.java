package com.genymobile.scrcpy;
import java.util.*;
import org.junit.Test;
import static org.junit.Assert.*;
public class NativeAlphaStructureRuleTest {
 @Test public void namedViewGroupIsNotACameraLeaf(){
  NativeAlphaProbe.Node container=new NativeAlphaProbe.Node(),child=new NativeAlphaProbe.Node();container.id="com.facebook.react.runtime.ReactSurfaceView@1";child.id="android.view.SurfaceView@2";child.parent=container;
  List<NativeAlphaProbe.Node> nodes=Arrays.asList(container,child);assertFalse(NativeAlphaPolicy.previewLeaf(nodes,container));assertTrue(NativeAlphaPolicy.previewLeaf(nodes,child));
 }
 @Test public void sceneFailureKeepsSpecificCode(){assertEquals("system_scene_scene_window_feature_unavailable",NativeAlphaPolicy.sceneFailureReason(new java.io.IOException("scene_window_feature_unavailable")));}
}
