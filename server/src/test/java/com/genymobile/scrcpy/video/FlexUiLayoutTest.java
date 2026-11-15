package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.control.PositionMapper;
import com.genymobile.scrcpy.model.Point;
import com.genymobile.scrcpy.model.Position;
import com.genymobile.scrcpy.model.Size;

import org.junit.Test;

import static org.junit.Assert.*;

public class FlexUiLayoutTest {
    private final FlexUiLayout tablet = new FlexUiLayout(new Size(3200, 2136), 440);

    @Test
    public void fittingDesktopKeepsTabletCanvasAndNativeDensity() {
        Size fitted = tablet.forViewport(new Size(1952, 1296));
        assertEquals(2136, fitted.getHeight());
        assertTrue(fitted.getWidth() >= 3200);
        assertEquals(440, tablet.getDpi());
        assertTrue(tablet.preservesNativeShortEdge(fitted));
    }

    @Test
    public void sameRatioWindowsHaveTheSameAndroidLayout() {
        Size original = tablet.forViewport(new Size(1600, 1068));
        for (int i = 0; i < 1000; i++) {
            assertEquals(original, tablet.forViewport(new Size(800, 534)));
            assertEquals(original, tablet.forViewport(new Size(3200, 2136)));
        }
    }

    @Test
    public void portraitAndUltrawideStillHaveNativeShortEdge() {
        Size portrait = tablet.forViewport(new Size(800, 1200));
        Size wide = tablet.forViewport(new Size(2400, 800));
        assertEquals(new Size(2136, 3204), portrait);
        assertEquals(new Size(6408, 2136), wide);
        assertEquals(tablet.forViewport(new Size(1200, 800)), portrait.rotate());
    }

    @Test
    public void extremeAspectRatioUsesBoundedMemoryAndReportsRelaxedFloor() {
        Size extreme = tablet.forViewport(new Size(40, 2000));
        assertTrue(extreme.getMax() <= 8192);
        assertTrue((long) extreme.getWidth() * extreme.getHeight() <= 20L * 1024 * 1024);
        assertFalse(tablet.preservesNativeShortEdge(extreme));
        assertEquals(440, tablet.getDpi());
    }

    @Test
    public void largeNativeDisplayAlsoRespectsThePixelBudget() {
        FlexUiLayout huge = new FlexUiLayout(new Size(8192, 8192), 640);
        Size size = huge.forViewport(new Size(1000, 1000));
        assertTrue((long) size.getWidth() * size.getHeight() <= 20L * 1024 * 1024);
    }

    @Test
    public void encoderResolutionDoesNotShrinkAndroidLogicalSpace() {
        Size viewport = new Size(1952, 1296);
        Size logical = tablet.forViewport(viewport);
        Size output = FlexUiLayout.videoSize(logical, viewport, new VideoConstraints(1280, 8, null));
        assertTrue(output.getMax() <= 1280);
        assertEquals(0, output.getWidth() % 8);
        assertEquals(0, output.getHeight() % 8);
        assertEquals(2136, logical.getHeight());
        assertTrue(tablet.preservesNativeShortEdge(logical));
    }

    @Test
    public void outputFollowsRotationInsteadOfStretchingBackToOldOrientation() {
        Size viewport = new Size(800, 1200);
        Size portrait = tablet.forViewport(viewport);
        VideoConstraints constraints = new VideoConstraints(0, 2, null);
        assertEquals(new Size(800, 1200), FlexUiLayout.videoSize(portrait, viewport, constraints));
        assertEquals(new Size(1200, 800), FlexUiLayout.videoSize(portrait.rotate(), viewport, constraints));
    }

    @Test
    public void resizedVideoStillMapsClicksToTheLogicalDisplay() {
        Size logical = tablet.forViewport(new Size(800, 1200));
        Size output = FlexUiLayout.videoSize(logical, new Size(800, 1200), new VideoConstraints(0, 2, null));
        VideoFilter filter = new VideoFilter(logical);
        filter.addResize(output);
        PositionMapper mapper = PositionMapper.create(output, filter.getInverseTransform(), logical);
        Point point = mapper.map(new Position(400, 600, 800, 1200));
        assertEquals(1068, point.getX());
        assertEquals(1602, point.getY());
        Point quarter = mapper.map(new Position(200, 300, 800, 1200));
        assertTrue(Math.abs(534 - quarter.getX()) <= 1);
        assertTrue(Math.abs(801 - quarter.getY()) <= 1);
        Point corner = mapper.map(new Position(799, 1199, 800, 1200));
        assertTrue(corner.getX() < logical.getWidth() && corner.getY() < logical.getHeight());
        assertNull(mapper.map(new Position(400, 600, 1200, 800)));
    }

    @Test
    public void phoneUsesItsOwnNativeCanvasRatherThanTabletThresholds() {
        FlexUiLayout phone = new FlexUiLayout(new Size(1440, 3200), 600);
        Size logical = phone.forViewport(new Size(582, 1296));
        assertEquals(1440, logical.getWidth());
        assertEquals(600, phone.getDpi());
        assertTrue(phone.preservesNativeShortEdge(logical));
    }

    @Test
    public void invalidMetricsNeverProduceZeroSizedOrOverflowedSurfaces() {
        assertThrows(IllegalArgumentException.class, () -> new FlexUiLayout(new Size(0, 3200), 440));
        assertThrows(IllegalArgumentException.class, () -> new FlexUiLayout(new Size(3200, 2136), 0));
        assertThrows(IllegalArgumentException.class, () -> tablet.forViewport(new Size(100, 0)));
        Size bounded = tablet.forViewport(new Size(Integer.MAX_VALUE, 1));
        assertTrue(bounded.getWidth() > 0 && bounded.getHeight() > 0);
        assertTrue(bounded.getMax() <= 8192);
    }

    @Test
    public void pixelMatchUsesWindowPixelsAndPreservesLogicalDp() {
        FlexUiLayout phone = new FlexUiLayout(new Size(1440, 3200), 600);
        FlexUiLayout.CaptureLayout layout = phone.forViewport(new Size(576, 1280), new Size(576, 1280));
        assertEquals(new Size(576, 1280), layout.getSize());
        assertEquals(240, layout.getDpi());
        assertEquals(384, layout.getSize().getWidth() * 160 / layout.getDpi());
        assertEquals(853, layout.getSize().getHeight() * 160 / layout.getDpi());
    }

    @Test
    public void smallerTransmittedVideoBoundsPngEvenWhenWindowIsLarger() {
        FlexUiLayout phone = new FlexUiLayout(new Size(1440, 3200), 600);
        FlexUiLayout.CaptureLayout layout = phone.forViewport(new Size(576, 1280), new Size(384, 854));
        assertEquals(new Size(384, 852), layout.getSize());
        assertEquals(160, layout.getDpi());
        FlexUiLayout.CaptureLayout legacy = phone.forViewport(new Size(576, 1280), (Size) null);
        assertEquals(phone.forViewport(new Size(576, 1280)), legacy.getSize());
        assertEquals(600, legacy.getDpi());
    }

    @Test
    public void windowPixelCapAndRotationUseTheSameMapping() {
        FlexUiLayout phone = new FlexUiLayout(new Size(1440, 3200), 600);
        FlexUiLayout.CaptureLayout portrait = phone.forViewport(new Size(576, 1280), new Size(1440, 3200));
        FlexUiLayout.CaptureLayout landscape = phone.forViewport(new Size(1280, 576), new Size(3200, 1440));
        assertEquals(portrait.getSize().rotate(), landscape.getSize());
        assertEquals(portrait.getDpi(), landscape.getDpi());
        FlexUiLayout.CaptureLayout tabletLayout = tablet.forViewport(new Size(1200, 800), new Size(1200, 800));
        assertTrue(Math.abs(2136 * 160.0 / 440 - tabletLayout.getSize().getHeight() * 160.0 / tabletLayout.getDpi()) < 3.4);
    }

    @Test
    public void resolutionMatchDoesNotImposeAnExtraQualityTierOrPixelBudget() {
        FlexUiLayout.CaptureLayout layout = tablet.forViewport(new Size(10000, 6000), new Size(3200, 1920));
        assertTrue((long) layout.getSize().getWidth() * layout.getSize().getHeight() > 1000000);
        assertTrue(layout.getSize().getWidth() <= 3200 && layout.getSize().getHeight() <= 1920);
        assertThrows(IllegalArgumentException.class, () -> tablet.forViewport(new Size(1200, 800), new Size(0, 1)));
    }
}
