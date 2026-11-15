package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.model.Size;

/** Keep the native logical short edge while the client changes the display aspect ratio. */
public final class FlexUiLayout {
    // Bound compositor allocation independently of the video encoder. Very extreme
    // aspect ratios must relax the native-size floor rather than allocate unbounded surfaces.
    private static final int MAX_EDGE = 8192;
    private static final long MAX_PIXELS = 20L * 1024 * 1024;
    private final int shortEdge;
    private final int dpi;

    public FlexUiLayout(Size mainSize, int mainDpi) {
        requireSize(mainSize);
        if (mainDpi <= 0) {
            throw new IllegalArgumentException("Invalid native density");
        }
        shortEdge = Math.min(mainSize.getWidth(), mainSize.getHeight());
        dpi = mainDpi;
    }

    public int getDpi() {
        return dpi;
    }

    /** Pixel dimensions and density must change together to preserve UI dp. */
    public static final class CaptureLayout {
        private final Size size;
        private final int density;

        private CaptureLayout(Size size, int density) {
            this.size = size;
            this.density = density;
        }

        public Size getSize() {
            return size;
        }

        public int getDpi() {
            return density;
        }
    }

    /** Null transmission size keeps the original native canvas policy. */
    public CaptureLayout forViewport(Size viewport, Size transmissionSize) {
        Size logical = forViewport(viewport);
        if (transmissionSize == null) {
            return new CaptureLayout(logical, dpi);
        }
        requireSize(transmissionSize);
        // Match the smaller of transmitted video pixels and desktop window pixels.
        // Panel resolution remains only a reference for preserving the app's dp UI.
        double scale = Math.min(Math.min((double) viewport.getWidth() / logical.getWidth(),
                (double) viewport.getHeight() / logical.getHeight()),
                Math.min((double) transmissionSize.getWidth() / logical.getWidth(),
                (double) transmissionSize.getHeight() / logical.getHeight()));
        scale = Math.min(scale, Math.min((double) MAX_EDGE / logical.getMax(),
                Math.sqrt((double) MAX_PIXELS / ((double) logical.getWidth() * logical.getHeight()))));
        int width = Math.max(2, (int) (logical.getWidth() * scale) / 2 * 2);
        int height = Math.max(2, (int) (logical.getHeight() * scale) / 2 * 2);
        int density = Math.max(1, (int) Math.round(dpi * scale));
        return new CaptureLayout(new Size(width, height), density);
    }

    public Size forViewport(Size viewport) {
        requireSize(viewport);
        double scale = (double) shortEdge / Math.min(viewport.getWidth(), viewport.getHeight());
        double width = viewport.getWidth() * scale;
        double height = viewport.getHeight() * scale;
        double limit = Math.min(1, Math.min(MAX_EDGE / Math.max(width, height), Math.sqrt(MAX_PIXELS / (width * height))));
        // Even dimensions avoid unnecessary half-pixel differences when rotating.
        int w = Math.max(2, (int) (width * limit) / 2 * 2);
        int h = Math.max(2, (int) (height * limit) / 2 * 2);
        return new Size(w, h);
    }

    public boolean preservesNativeShortEdge(Size size) {
        return Math.min(size.getWidth(), size.getHeight()) >= shortEdge - 1;
    }

    /** Use the current orientation, including rotations requested by the app. */
    public static Size videoSize(Size content, Size viewport, VideoConstraints constraints) {
        requireSize(content);
        requireSize(viewport);
        double scale = (double) viewport.getMax() / content.getMax();
        Size requested = new Size(Math.max(1, (int) Math.round(content.getWidth() * scale)),
                Math.max(1, (int) Math.round(content.getHeight() * scale)));
        return requested.constrain(constraints);
    }

    private static void requireSize(Size size) {
        if (size == null || size.getWidth() <= 0 || size.getHeight() <= 0) {
            throw new IllegalArgumentException("Invalid viewport");
        }
    }
}
