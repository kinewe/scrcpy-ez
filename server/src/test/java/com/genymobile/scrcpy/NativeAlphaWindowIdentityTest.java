package com.genymobile.scrcpy;

public final class NativeAlphaWindowIdentityTest {
    public static void main(String[] args) {
        String pkg = "org.example.camera", activity = pkg + ".Scanner";
        String component = pkg + "/" + activity;
        for (String name : new String[]{component, "c5fa3ca " + component, "ABCDEF " + pkg + "/.Scanner", component + "#67264"}) {
            if (!activity.equals(NativeAlphaWindowIdentity.activity(name, pkg))) throw new AssertionError("main window missed: " + name);
        }
        for (String name : new String[]{"3594650 Panel:" + component, "Panel:" + component,
                "deadbeef PopupWindow:" + component, "deeb47b SurfaceView[" + component + "](BLAST)#67267",
                "Background for SurfaceView[" + component + "]#67268", "9e46655 ActivityRecordInputSink " + component,
                "prefix" + component, "org.other/" + activity, pkg + "/", pkg + "/Scanner]", ""}) {
            if (NativeAlphaWindowIdentity.activity(name, pkg) != null) throw new AssertionError("attached/unowned window accepted: " + name);
        }
        if (NativeAlphaWindowIdentity.activity(null, pkg) != null) throw new AssertionError("null name");
        System.out.println("PASS NativeAlphaWindowIdentity: main, short component, hex identity, attached Panel/Popup, SurfaceView, InputSink and other package");
    }
}
