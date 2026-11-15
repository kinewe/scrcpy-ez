package com.genymobile.scrcpy;

import java.util.Map;

/** Evidence about the Drawable itself. View.isOpaque() cannot establish its absence. */
final class NativeAlphaBackgroundEvidence {
    static boolean isAbsent(String dumpedBackground, Map<String, ?> fields) {
        Object drawable = fields == null ? null : fields.get("miui:backgroundDrawableName");
        if (drawable instanceof String) {
            // An explicitly exported empty drawable name means no background.
            // Missing metadata is unknown; a concrete class always means present.
            return ((String) drawable).isEmpty() || "null".equals(drawable);
        }
        return "null".equals(dumpedBackground);
    }

    private NativeAlphaBackgroundEvidence() {}
}
