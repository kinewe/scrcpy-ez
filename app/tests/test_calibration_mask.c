#include <assert.h>
#include <math.h>
#include <stdio.h>
#include "calibration_mask_state.h"

int main(void) {
    struct sc_calibration_mask_state s = {0};
    sc_tick t = SC_TICK_FROM_SEC(10);
    sc_calibration_mask_advance(&s, false, true, false, t);
    assert(s.phase == SC_MASK_HIDDEN);
    sc_calibration_mask_advance(&s, true, true, false, t);
    assert(sc_calibration_mask_alpha(&s, t) == 1);
    sc_calibration_mask_advance(&s, true, true, false, t + SC_TICK_FROM_MS(500));
    assert(s.animation_start == t); // repeated frames must not restart rotation
    t += SC_TICK_FROM_SEC(1);
    sc_calibration_mask_advance(&s, true, false, false, t);
    assert(s.phase == SC_MASK_COVERING);
    assert(sc_calibration_mask_alpha(&s, t + SC_TICK_FROM_MS(150)) == 1);
    t += SC_TICK_FROM_MS(150);
    sc_calibration_mask_advance(&s, true, false, true, t);
    assert(s.phase == SC_MASK_FADING);
    assert(fabsf(sc_calibration_mask_alpha(&s, t + SC_TICK_FROM_MS(90)) - .25f) < .001f);

    // The scan result can replace the composite before Android closes the
    // camera. Losing the frame must neither reverse nor restart the fade.
    sc_calibration_mask_advance(&s, true, false, false, t + SC_TICK_FROM_MS(100));
    assert(s.phase == SC_MASK_FADING && s.transition_start == t);
    sc_calibration_mask_advance(&s, true, false, false, t + SC_TICK_FROM_MS(180));
    assert(s.phase == SC_MASK_HIDDEN);
    sc_calibration_mask_advance(&s, true, false, false, t + SC_TICK_FROM_MS(350));
    assert(s.phase == SC_MASK_HIDDEN);
    sc_calibration_mask_advance(&s, false, false, false, t + SC_TICK_FROM_MS(400));
    assert(s.phase == SC_MASK_HIDDEN);
    sc_calibration_mask_advance(&s, true, false, false, t + SC_TICK_FROM_MS(500));
    assert(s.phase == SC_MASK_COVERING); // scanner re-opened

    // A fresh calibration during the fade returns immediately to full cover.
    t += SC_TICK_FROM_SEC(1);
    sc_calibration_mask_advance(&s, true, true, false, t);
    sc_calibration_mask_advance(&s, true, false, true, t + 1);
    sc_calibration_mask_advance(&s, true, true, false, t + SC_TICK_FROM_MS(40));
    assert(sc_calibration_mask_alpha(&s, t + SC_TICK_FROM_MS(40)) == 1);
    sc_calibration_mask_step(&s, false, false, false, false, true, t + SC_TICK_FROM_MS(45));
    assert(s.phase == SC_MASK_HIDDEN); // disconnection or camera eviction

    // A real 0 -> 3 camera restart and a system scene must keep one animation.
    t += SC_TICK_FROM_SEC(1);
    sc_calibration_mask_step(&s, true, false, false, false, false, t);
    sc_calibration_mask_step(&s, false, false, false, false, false, t + SC_TICK_FROM_MS(100));
    sc_calibration_mask_step(&s, true, false, false, false, false, t + SC_TICK_FROM_MS(500));
    sc_calibration_mask_step(&s, false, true, false, false, false, t + SC_TICK_FROM_SEC(5));
    assert(s.phase == SC_MASK_COVERING && s.animation_start == t);
    // Scene restored, then native source released and compatibility prepared.
    sc_calibration_mask_step(&s, true, true, false, false, false, t + SC_TICK_FROM_SEC(9));
    sc_calibration_mask_step(&s, true, false, false, false, false, t + SC_TICK_FROM_SEC(12));
    assert(s.phase == SC_MASK_COVERING && s.animation_start == t);
    sc_calibration_mask_step(&s, true, false, false, true, false, t + SC_TICK_FROM_SEC(13));
    assert(s.phase == SC_MASK_FADING && !s.timed_out);
    sc_calibration_mask_step(&s, true, false, false, true, false, t + SC_TICK_FROM_MS(13180));
    assert(s.phase == SC_MASK_HIDDEN);
    sc_calibration_mask_step(&s, true, true, false, false, false, t + SC_TICK_FROM_MS(13200));
    assert(s.phase == SC_MASK_COVERING); // explicit fallback after a usable frame
    sc_calibration_mask_step(&s, false, false, false, false, true, t + SC_TICK_FROM_MS(13250));
    assert(s.phase == SC_MASK_HIDDEN);

    // Exiting the scanner does not retain a cover once the camera gap ends.
    t += SC_TICK_FROM_SEC(14);
    sc_calibration_mask_step(&s, true, false, false, false, false, t);
    sc_calibration_mask_step(&s, false, false, false, false, false, t + SC_TICK_FROM_MS(100));
    sc_calibration_mask_step(&s, false, false, false, false, false, t + SC_TICK_FROM_MS(750));
    assert(s.phase == SC_MASK_HIDDEN);

    // Watchdog releases even while a handoff is stuck. Repeated ticks cannot
    // restart the expired cover. A new camera request can start a new cover.
    t += SC_TICK_FROM_SEC(1);
    sc_calibration_mask_step(&s, true, false, false, false, false, t);
    sc_calibration_mask_step(&s, false, true, false, false, false, t + SC_CALIBRATION_MASK_HOLD_CAP);
    assert(s.phase == SC_MASK_FADING && s.timed_out);
    sc_calibration_mask_step(&s, false, true, false, false, false, t + SC_TICK_FROM_SEC(21));
    assert(s.phase == SC_MASK_EXPIRED && !sc_calibration_mask_visible(&s));
    sc_calibration_mask_step(&s, true, true, false, false, false, t + SC_TICK_FROM_SEC(22));
    assert(s.phase == SC_MASK_EXPIRED);
    sc_calibration_mask_step(&s, false, false, false, false, false, t + SC_TICK_FROM_SEC(23));
    sc_calibration_mask_step(&s, true, false, false, false, false, t + SC_TICK_FROM_SEC(24));
    assert(s.phase == SC_MASK_COVERING && !s.timed_out);
    sc_calibration_mask_step(&s, true, true, false, false, true, t + SC_TICK_FROM_SEC(25));
    assert(s.phase == SC_MASK_HIDDEN);
    puts("calibration mask: scan completion, reentry, handoff, fade, cancellation and watchdog passed");
    return 0;
}
