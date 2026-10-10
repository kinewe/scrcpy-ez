package com.genymobile.scrcpy;

import org.junit.Test;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public class AppReuseOptionsTest {
    @Test public void reusePolicyIsOptInForOrdinaryWindowsOnly() {
        assertFalse(Options.parse(BuildConfig.VERSION_NAME).getReuseAppTask());
        assertTrue(Options.parse(BuildConfig.VERSION_NAME, "reuse_app_task=true").getReuseAppTask());
        assertFalse(Options.parse(BuildConfig.VERSION_NAME, "reuse_app_task=false").getReuseAppTask());
    }
}
