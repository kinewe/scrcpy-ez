package com.genymobile.scrcpy;

import java.util.HashMap;
import java.util.Map;

public final class NativeAlphaBackgroundEvidenceTest {
    public static void main(String[] args) {
        Map<String, Object> snapshot = new HashMap<>();
        snapshot.put("drawing:opaque", false);
        snapshot.put("miui:backgroundDrawableName", "android.graphics.drawable.LayerDrawable");
        if (NativeAlphaBackgroundEvidence.isAbsent(null, snapshot)) throw new AssertionError("WeCom root LayerDrawable was skipped again");
        if (NativeAlphaBackgroundEvidence.isAbsent("null", snapshot)) throw new AssertionError("present Drawable must win over conflicting dump");
        snapshot.put("miui:backgroundDrawableName", "");
        if (!NativeAlphaBackgroundEvidence.isAbsent(null, snapshot)) throw new AssertionError("observed empty ancestor background rejected");
        snapshot.remove("miui:backgroundDrawableName");
        if (NativeAlphaBackgroundEvidence.isAbsent(null, snapshot)) throw new AssertionError("View.isOpaque=false is not background evidence");
        if (NativeAlphaBackgroundEvidence.isAbsent(null, null)) throw new AssertionError("missing metadata is not absence");
        if (!NativeAlphaBackgroundEvidence.isAbsent("null", null)) throw new AssertionError("explicit dumped null lost");
        snapshot.put("miui:backgroundDrawableName", "android.graphics.drawable.ColorDrawable");
        if (NativeAlphaBackgroundEvidence.isAbsent(null, snapshot)) throw new AssertionError("colour Drawable treated as missing");
        System.out.println("PASS NativeAlphaBackgroundEvidence: actual WeCom root/ancestor snapshots, conflicting and missing metadata");
    }
}
