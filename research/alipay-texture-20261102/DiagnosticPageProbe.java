package com.genymobile.scrcpy;
import android.os.*;
/** Android-only page diagnostic; never used as a stream or throughput sample. */
public final class DiagnosticPageProbe {
    public static void main(String[] args)throws Exception{
        if(android.os.Process.myUid()!=2000||args.length!=2)throw new IllegalArgumentException();
        Looper.prepareMainLooper();Workarounds.apply();
        try(SceneContext scene=new SceneContext(args[0],false)){
            if(scene.display!=Integer.parseInt(args[1]))throw new SecurityException("different display");
            byte[] png=WindowProbe.command(scene.window,"CAPTURE",scene.nodes.get(0).id,32*1024*1024,6000);
            try(java.io.FileOutputStream stream=new java.io.FileOutputStream("/data/local/tmp/rc25-temporal-page.png")){stream.write(png);}
            System.out.println("DIAGNOSTIC_PAGE display="+scene.display+" activity="+scene.activity);
        }
        System.exit(0);
    }
}
