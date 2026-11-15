package com.genymobile.scrcpy.video;

import org.junit.Test;
import static org.junit.Assert.*;

public class BacklogAbrTest {
    private static BacklogAbr policy() { return new BacklogAbr(60000000, 144, false, false); }
    private void warm(BacklogAbr p) {
        for (int t = 0; t < 1200; t += 10) {
            assertNull(p.observe(t, 900000, t * 1000L, 0, new ClientBacklog.Sample(1,t,0,0,1,t)));
        }
    }
    @Test public void shortJitterAndContentCadenceNeverDegrade() {
        BacklogAbr p = policy(); warm(p);
        for (int t = 1200; t < 10000; t += 10) {
            assertNull(p.observe(t, 900000 + (t % 50), t * 1000L, 0, null));
        }
        assertEquals(60000000, p.bitrate()); assertEquals(144, p.fps(), .01);
    }
    @Test public void sustainedSourceBacklogLowersFramesNotBitrate() {
        BacklogAbr p = policy(); warm(p);
        BacklogAbr.Decision d = null;
        for (int t = 1200; t <= 1550; t += 10) d = p.observe(t, 900090, t * 1000L, 0, null);
        assertNotNull(d); assertEquals("source-backlog", d.reason);
        assertEquals(60000000, d.bitrate); assertTrue(d.fps < 100);
    }

    private BacklogAbr reducedFrameRate() {
        BacklogAbr p = new BacklogAbr(15000000, 60, false, false);
        warm(p);
        for (int t = 1200; t <= 1550; t += 10) {
            p.observe(t, 900090, t * 1000L, 0, null);
        }
        assertEquals(45, p.fps(), .01);
        return p;
    }

    @Test public void drainedStaticClientRecoversWithoutEncodedFrames() {
        BacklogAbr p = reducedFrameRate();
        ClientBacklog client = new ClientBacklog();
        for (int t = 2800; t <= 4800; t += 200) {
            client.update(1, t, 0, 80, ClientBacklog.IDLE, t);
            BacklogAbr.Decision decision = p.observeIdle(t, client.get());
            if (t < 4800) {
                assertNull(decision);
            } else {
                assertNotNull(decision);
                assertEquals("idle-recovery", decision.reason);
                assertEquals(15000000, decision.bitrate);
                assertEquals(60, decision.fps, .01);
            }
        }
        assertFalse(p.canRecoverFps());
    }

    @Test public void idleRecoveryRequiresFreshFeedbackFromOneEpoch() {
        BacklogAbr p = reducedFrameRate();
        ClientBacklog client = new ClientBacklog();
        client.update(1, 1, 0, 0, ClientBacklog.IDLE, 2800);
        assertNull(p.observeIdle(2800, client.get()));
        assertNull(p.observeIdle(4800, client.get()));
        for (int t = 5000; t <= 7000; t += 200) {
            int epoch = t < 6800 ? 1 : 2;
            int queueCount = t == 6400 ? 1 : 0;
            client.update(epoch, t, 0, 0, ClientBacklog.IDLE, queueCount, 0, t);
            assertNull(p.observeIdle(t, client.get()));
        }
        assertEquals(45, p.fps(), .01);
    }
    @Test public void oneCachedReportNeverCountsAsSustained() {
        BacklogAbr p = policy(); warm(p);
        ClientBacklog b = new ClientBacklog(); b.update(1, 1, 300, 0, 1, 1200);
        for (int t = 1200; t < 2200; t += 10) assertNull(p.observe(t, 900000, t * 1000L, 0, b.get()));
    }
    @Test public void networkPressureLowersBitrateOnly() {
        BacklogAbr p = policy(); warm(p);
        ClientBacklog b = new ClientBacklog(); BacklogAbr.Decision d = null;
        for (int t = 1200; t <= 1600; t += 200) {
            b.update(1, t, 0, 90, 3, t); d = p.observe(t, 900000, t * 1000L, 0, b.get());
        }
        assertNotNull(d); assertEquals("delivery-backlog", d.reason);
        assertEquals(51000000, d.bitrate); assertEquals(144, d.fps, .01);
    }
    @Test public void decoderPressureWorksForHardwareAndSoftware() {
        for (int flags : new int[] {1, 3}) {
            BacklogAbr p = policy(); warm(p); ClientBacklog b = new ClientBacklog(); BacklogAbr.Decision d = null;
            for (int t = 1200; t <= 1600; t += 200) {
                b.update(1, t, 90, 0, flags, t); d = p.observe(t, 900000, t * 1000L, 0, b.get());
            }
            assertNotNull(d); assertEquals("decode-backlog", d.reason); assertEquals(60000000, d.bitrate);
        }
    }
    @Test public void intentionalWaitDoesNotBecomeDecoderPressure() {
        BacklogAbr p = policy(); warm(p); ClientBacklog b = new ClientBacklog();
        for (int t = 1200; t <= 2400; t += 200) {
            b.update(1, t, 900, 0, 7, t); assertNull(p.observe(t, 900000, t * 1000L, 0, b.get()));
        }
    }
    @Test public void locksAndNonstandardCeilingsRemainExact() {
        for (float limit : new float[] {24, 45, 75, 120, 144}) {
            BacklogAbr p = new BacklogAbr(60000000, limit, true, true); warm(p);
            ClientBacklog b = new ClientBacklog();
            for (int t = 1200; t < 8000; t += 200) {
                b.update(1, t, 250, 250, 1, t); assertNull(p.observe(t, 900250, t * 1000L, 0, b.get()));
            }
            assertEquals(limit, p.fps(), .01); assertEquals(60000000, p.bitrate());
        }
    }
    @Test public void mailboxRejectsStaleMalformedAndCrossEpochReports() {
        ClientBacklog b = new ClientBacklog(); b.update(2, 10, 30, 20, 1, 1000);
        b.update(1, 100, 900, 900, 1, 1100); b.update(2, 9, 900, 900, 1, 1100);
        b.update(2, 11, -1, 900, 1, 1100); b.update(2, 11, 900, 900, 99, 1100);
        assertEquals(10, b.get().sequence); assertTrue(b.get().valid(1800)); assertFalse(b.get().valid(1801));
        b.update(3, 1, 0, 0, 1, 1900); assertEquals(3, b.get().epoch);
    }
    @Test public void recoveryIsBoundedAndAnActuatorFailureCanRollBack() {
        BacklogAbr p = policy(); warm(p); ClientBacklog b = new ClientBacklog();
        BacklogAbr.Decision cut = null;
        for (int t = 1200; t <= 1600; t += 200) {
            b.update(1, t, 0, 90, 1, t); cut = p.observe(t, 900000, t * 1000L, 0, b.get());
        }
        assertNotNull(cut);
        for (int t = 1800; t < 3600; t += 200) {
            b.update(1, t, 0, 0, 1, t); BacklogAbr.Decision d = p.observe(t, 900000, t * 1000L, 0, b.get());
            if (d != null) assertTrue(d.bitrate <= 56100000);
        }
        p.reject(60000000, 144); assertEquals(60000000, p.bitrate()); assertEquals(144, p.fps(), .01);
    }
    @Test public void socketBlockingWorksWithoutAControlChannel() {
        BacklogAbr p = policy(); warm(p);
        BacklogAbr.Decision d = null;
        for (int t=1200; t<=1550; t+=10) d=p.observe(t,900000,t*1000L,90,null);
        assertNotNull(d); assertEquals("delivery-backlog",d.reason); assertEquals(51000000,d.bitrate);
    }
    @Test public void bitrateFloorStillHasAFrameRateEscape() {
        BacklogAbr p = new BacklogAbr(1000000,120,false,false); warm(p);
        BacklogAbr.Decision d = null;
        for (int t=1200; t<=1550; t+=10) d=p.observe(t,900000,t*1000L,90,null);
        assertNotNull(d); assertEquals(1000000,d.bitrate); assertTrue(d.fps<100);
    }
    @Test public void isolatedLargeDelayAndClockResetDoNotBecomePersistent() {
        BacklogAbr p=policy(); warm(p);
        assertNull(p.observe(1200,900300,1200000,0,null));
        assertNull(p.observe(1210,900000,1210000,0,null));
        assertNull(p.observe(1220,900000,1,0,null));
        assertEquals(60000000,p.bitrate()); assertEquals(144,p.fps(),.01);
    }
    @Test public void coldReceiverInitializationGetsItsOwnGracePeriod() {
        BacklogAbr p=policy(); warm(p);
        for (int t=1200;t<1800;t+=200) {
            ClientBacklog.Sample s=new ClientBacklog.Sample(2,t,400,0,3,t);
            assertNull(p.observe(t,900000,t*1000L,0,s));
        }
        for (int t=1800;t<3000;t+=200) {
            assertNull(p.observe(t,900000,t*1000L,0,new ClientBacklog.Sample(2,t,0,0,3,t)));
        }
    }
    @Test public void repeatedFailedFrameRateProbesBackOff() {
        BacklogAbr p=policy(); warm(p); ClientBacklog b=new ClientBacklog();
        java.util.List<Long> probes=new java.util.ArrayList<>();
        for (int t=1200;t<60000;t+=10) {
            if (t%200==0) b.update(1,t,p.fps()>30?120:0,0,3,t);
            BacklogAbr.Decision d=p.observe(t,900000,t*1000L,0,b.get());
            if (d!=null && d.reason.equals("healthy-recovery") && d.fps==45) probes.add((long)t);
        }
        assertTrue(probes.size()>=3);
        assertTrue(probes.get(2)-probes.get(1)>probes.get(1)-probes.get(0));
    }
    @Test public void decliningOldQueueDoesNotCauseRepeatedFrameRateCuts() {
        BacklogAbr p=policy(); warm(p); ClientBacklog b=new ClientBacklog();
        BacklogAbr.Decision d=null;
        for (int t=1200;t<=1600;t+=200) {
            b.update(1,t,1000,0,3,120,25000,t);
            BacklogAbr.Decision next=p.observe(t,900000,t*1000L,0,b.get());
            if(next!=null) d=next;
        }
        assertNotNull(d); assertEquals(30,d.fps,.01);
        for (int t=1800;t<7000;t+=200) {
            b.update(1,t,1000+(t-1800)/10,0,3,120-(t-1600)/100,25000,t);
            assertNull(p.observe(t,900000,t*1000L,0,b.get()));
        }
        for (int t=7000;t<16000;t+=200) {
            b.update(1,t,0,0,3,0,25000,t);
            assertNull(p.observe(t,900000,t*1000L,0,b.get()));
        }
        assertEquals(30,p.fps(),.01);
    }
    @Test public void BitrateRecoveryCannotStarveFrameRateRecovery() {
        BacklogAbr p=policy(); warm(p); ClientBacklog b=new ClientBacklog();
        p.reject(1000000,30);
        for (int t=1200;t<15000;t+=200) {
            b.update(1,t,0,0,3,t);
            p.observe(t,900000,t*1000L,0,b.get());
        }
        assertEquals(144,p.fps(),.01);
        assertTrue(p.bitrate()<60000000);
    }
    @Test public void confirmedSlowDecoderCanUseEmergencyPathDuringWarmup() {
        BacklogAbr p=policy(); warm(p);
        assertNull(p.observe(1200,900000,1200000,0,new ClientBacklog.Sample(2,1,400,0,3,40,25000,1200)));
        BacklogAbr.Decision d=p.observe(1400,900000,1400000,0,new ClientBacklog.Sample(2,2,600,0,3,60,25000,1400));
        assertNotNull(d); assertEquals(30,d.fps,.01);
    }
    @Test public void deliveryBudgetUsesMeasuredTrafficInsteadOfTheConfiguredCeiling() {
        BacklogAbr p=policy(); warm(p);
        assertNull(p.observe(1200,900000,1200000,0,2000000,new ClientBacklog.Sample(1,1,0,300,3,0,1000,500000,1200)));
        BacklogAbr.Decision d=p.observe(1400,900000,1400000,0,2000000,new ClientBacklog.Sample(1,2,0,500,3,0,1000,500000,1400));
        assertNotNull(d); assertEquals(1000000,d.bitrate); assertEquals(144,d.fps,.01);
        for (int t=1600;t<5000;t+=200) {
            assertNull(p.observe(t,900300,t*1000L,0,300000,new ClientBacklog.Sample(1,t,0,500,3,0,1000,500000,t)));
        }
        assertEquals(144,p.fps(),.01);
    }
    @Test public void codedRateWindowsResetAndPreserveMediaTime() {
        EncodedRate rate=new EncodedRate();
        for(int i=0;i<=100;++i) rate.add(i*10000L,1000);
        assertEquals(800000,rate.get());
        rate.add(1,1000); assertEquals(0,rate.get());
        rate.add(9000000,1000); assertEquals(0,rate.get());
    }
    @Test public void constantPathDelayDoesNotCauseAnEndlessDownshift() {
        BacklogAbr p=policy(); warm(p); int cuts=0;
        for(int t=1200;t<12000;t+=200) {
            BacklogAbr.Decision d=p.observe(t,900000,t*1000L,0,300000,
                    new ClientBacklog.Sample(1,t,0,300,3,0,1000,300000,t));
            if(d!=null && d.reason.equals("delivery-backlog")) ++cuts;
        }
        assertEquals(1,cuts); assertEquals(144,p.fps(),.01); assertTrue(p.bitrate()>1000000);
    }
    @Test public void feedbackUnsignedOrderingAlsoWorksWithoutNewAndroidApis() {
        ClientBacklog b=new ClientBacklog();
        b.update(1,Integer.MAX_VALUE,0,0,3,1000);
        b.update(1,Integer.MIN_VALUE,20,0,3,1200);
        assertEquals(20,b.get().queueMs);
        b.update(1,Integer.MAX_VALUE,999,0,3,1400);
        assertEquals(20,b.get().queueMs);
    }
    @Test public void rejectedBitrateActuatorUsesTheUnlockedFrameRateDimension() {
        BacklogAbr p=policy(); warm(p);
        p.observe(1200,900000,1200000,0,2000000,new ClientBacklog.Sample(1,1,0,300,3,0,1000,500000,1200));
        assertNotNull(p.observe(1400,900000,1400000,0,2000000,new ClientBacklog.Sample(1,2,0,500,3,0,1000,500000,1400)));
        p.rejectBitrate(60000000,144);
        BacklogAbr.Decision result=null;
        for(int t=1600;t<=2200;t+=200) {
            BacklogAbr.Decision d=p.observe(t,900000,t*1000L,0,2000000,
                    new ClientBacklog.Sample(1,t,0,500,3,0,1000,500000,t));
            if(d!=null) result=d;
        }
        assertNotNull(result); assertEquals(60000000,result.bitrate); assertTrue(result.fps<30);
    }
}
