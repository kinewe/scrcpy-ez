#ifndef SC_CALIBRATION_MASK_H
#define SC_CALIBRATION_MASK_H

#include <SDL3/SDL.h>
#include "calibration_mask_state.h"

struct sc_calibration_mask {
    struct sc_calibration_mask_state state;
    SDL_Texture *layers[5];
    SDL_Texture *caption;
    bool load_attempted;
};

#ifdef _WIN32
bool sc_calibration_mask_draw(struct sc_calibration_mask *, SDL_Renderer *,
                              const SDL_FRect *, sc_tick);
void sc_calibration_mask_destroy(struct sc_calibration_mask *);
#else
static inline void
sc_calibration_mask_destroy(struct sc_calibration_mask *mask) {
    (void) mask;
}
#endif

#endif
