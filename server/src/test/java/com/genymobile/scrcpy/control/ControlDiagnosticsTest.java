package com.genymobile.scrcpy.control;

import org.junit.Test;

import static org.junit.Assert.assertTrue;

public class ControlDiagnosticsTest {
    @Test
    public void distinguishesNoInputBlockedInjectionAndRejectedInput() {
        ControlDiagnostics diagnostics = new ControlDiagnostics();
        assertTrue(diagnostics.snapshot(0).contains("heartbeat_age_ms=-1"));
        assertTrue(diagnostics.snapshot(0).contains("input_age_ms=-1"));
        diagnostics.heartbeat(1000);
        diagnostics.received(2000);
        String blocked = diagnostics.snapshot(2500);
        assertTrue(blocked.contains("heartbeats=1 heartbeat_age_ms=1500"));
        assertTrue(blocked.contains("input_received=1 input_completed=0 input_rejected=0 input_pending=1 input_age_ms=500"));
        diagnostics.completed(false);
        diagnostics.received(3000);
        diagnostics.completed(true);
        String completed = diagnostics.snapshot(4000);
        assertTrue(completed.contains("input_received=2 input_completed=2 input_rejected=1 input_pending=0 input_age_ms=1000"));
    }
}
