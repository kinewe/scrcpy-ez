#ifndef SC_CALIBRATION_MASK_STATE_H
#define SC_CALIBRATION_MASK_STATE_H

#include <stdbool.h>
#include "util/tick.h"

#define SC_CALIBRATION_MASK_FADE SC_TICK_FROM_MS(180)
#define SC_CALIBRATION_MASK_CAMERA_GAP SC_TICK_FROM_MS(650)
#define SC_CALIBRATION_MASK_HOLD_CAP SC_TICK_FROM_SEC(20)

struct sc_calibration_mask_state {
    enum {
        SC_MASK_HIDDEN,
        SC_MASK_COVERING,
        SC_MASK_FADING,
        SC_MASK_EXPIRED,
    } phase;
    sc_tick animation_start;
    sc_tick transition_start;
    sc_tick inactive_since;
    bool inactive;
    bool timed_out;
    bool preview_seen;
};

static inline bool
sc_calibration_mask_visible(const struct sc_calibration_mask_state *s) {
    return s->phase == SC_MASK_COVERING || s->phase == SC_MASK_FADING;
}

static inline float
sc_calibration_mask_alpha(const struct sc_calibration_mask_state *s,
                          sc_tick now) {
    if (s->phase == SC_MASK_COVERING) {
        return 1;
    }
    if (s->phase != SC_MASK_FADING) {
        return 0;
    }
    float remaining = 1.f - (float) (now - s->transition_start)
                           / SC_CALIBRATION_MASK_FADE;
    return remaining > 0 ? remaining * remaining : 0;
}

static inline void
sc_calibration_mask_step(struct sc_calibration_mask_state *s, bool active,
                         bool handoff, bool preparing, bool ready, bool stopped,
                         sc_tick now) {
    if (stopped || (s->inactive && !handoff
            && now - s->inactive_since >= SC_CALIBRATION_MASK_CAMERA_GAP)) {
        *s = (struct sc_calibration_mask_state) {0};
    }
    if (stopped) {
        return;
    }
    if (!active && !handoff) {
        if (s->phase == SC_MASK_HIDDEN || (s->phase == SC_MASK_FADING && !s->timed_out)) {
            *s = (struct sc_calibration_mask_state) {0};
            return;
        }
        if (!s->inactive) {
            s->inactive = true;
            s->inactive_since = now;
        }
    } else {
        s->inactive = false;
    }

    if (ready) {
        s->preview_seen = true;
    }
    // The scanner can stop producing usable frames before its camera closes.
    // Once shown, only real preparation or a handoff can open another cover.
    bool start_cover = !ready && (active || handoff)
                    && (!s->preview_seen || preparing || handoff);
    if (s->phase == SC_MASK_FADING) {
        if (!s->timed_out && start_cover) {
            s->phase = SC_MASK_HIDDEN;
        } else if (now - s->transition_start >= SC_CALIBRATION_MASK_FADE) {
            s->phase = s->timed_out ? SC_MASK_EXPIRED : SC_MASK_HIDDEN;
        } else {
            return;
        }
    }
    if (s->phase == SC_MASK_EXPIRED) {
        if (ready) {
            *s = (struct sc_calibration_mask_state) {.preview_seen = true};
        }
        return;
    }
    if (s->phase == SC_MASK_HIDDEN) {
        if (!start_cover) {
            return;
        }
        s->phase = SC_MASK_COVERING;
        s->preview_seen = false;
        s->animation_start = now;
    }
    if (ready || now - s->animation_start >= SC_CALIBRATION_MASK_HOLD_CAP) {
        s->timed_out = !ready;
        s->phase = SC_MASK_FADING;
        s->transition_start = now;
    }
}

static inline void
sc_calibration_mask_advance(struct sc_calibration_mask_state *s, bool active,
                            bool calibrating, bool preview_ready, sc_tick now) {
    sc_calibration_mask_step(s, active, false, calibrating,
                             preview_ready && !calibrating, false, now);
}

#endif
