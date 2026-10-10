package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.model.Size;

/** Automatic GUI density scales from a constant basis, avoiding resize rounding drift. */
public final class FlexDisplayDensity {
    private final int longEdge;
    private final int dpi;

    public FlexDisplayDensity(Size initialSize, int initialDpi) {
        longEdge = initialSize.getMax();
        dpi = initialDpi;
        if (longEdge <= 0 || dpi <= 0) {
            throw new IllegalArgumentException("Invalid display density basis");
        }
    }

    public int forSize(Size size) {
        long scaled = (long) dpi * size.getMax() / longEdge;
        return (int) Math.max(1L, Math.min(Integer.MAX_VALUE, scaled));
    }
}
