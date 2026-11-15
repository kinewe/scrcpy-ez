package com.genymobile.scrcpy.video;
import com.genymobile.scrcpy.model.Size;
import org.junit.Test;
import static org.junit.Assert.assertEquals;
public class InitialDisplayLayoutTest {
 @Test public void currentLogicalShapeWinsOverTransportShape(){
  int[][] cases={{1920,864,1440,3200,864,1920},{864,1920,3200,1440,1920,864},{1920,1280,2136,3200,1280,1920},{1920,1280,3200,2136,1920,1280},{1000,1000,1440,3200,1000,1000},{1920,864,1000,1000,1920,864}};
  for(int[] c:cases){Size got=InitialDisplayLayout.orientLike(new Size(c[0],c[1]),new Size(c[2],c[3]));assertEquals(c[4],got.getWidth());assertEquals(c[5],got.getHeight());}
 }
}
