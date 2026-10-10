package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.model.Size;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

public class FlexDisplayDensityTest {
    @Test
    public void fittedPortraitPreservesLogicalWidth() {
        FlexDisplayDensity density = new FlexDisplayDensity(new Size(864, 1920), 360);
        int fitted = density.forSize(new Size(582, 1296));
        assertEquals(243, fitted);
        assertTrue(Math.abs(864 * 160.0 / 360 - 582 * 160.0 / fitted) < 1);
    }

    @Test
    public void resizeAndRotationNeverAccumulateRoundingDrift() {
        FlexDisplayDensity density = new FlexDisplayDensity(new Size(864, 1920), 360);
        for (int i = 0; i < 1000; ++i) {
            assertEquals(243, density.forSize(new Size(1296, 582)));
            assertEquals(360, density.forSize(new Size(864, 1920)));
        }
    }

    @Test
    public void initialVideoConstraintScalesFromOriginalBasis() {
        FlexDisplayDensity density = new FlexDisplayDensity(new Size(1152, 2560), 480);
        assertEquals(240, density.forSize(new Size(576, 1280)));
        assertEquals(480, density.forSize(new Size(1152, 2560)));
    }

    @Test
    public void arithmeticDoesNotOverflowOrYieldZero() {
        FlexDisplayDensity density = new FlexDisplayDensity(new Size(1, 2), 480);
        assertEquals(Integer.MAX_VALUE, density.forSize(new Size(2, Integer.MAX_VALUE)));
        density = new FlexDisplayDensity(new Size(864, 1920), 1);
        assertEquals(1, density.forSize(new Size(2, 2)));
    }
}
