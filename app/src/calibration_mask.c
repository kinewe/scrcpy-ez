#include "calibration_mask.h"

#include <math.h>
#include <stdlib.h>
#include "calibration_art.h"
#include "util/file.h"
#include "util/log.h"

static SDL_Texture *
load_art(SDL_Renderer *renderer, const char *filename, SDL_ScaleMode scale) {
    char *path = sc_file_get_local_path(filename);
    if (!path) {
        return NULL;
    }
    SDL_Surface *surface = SDL_LoadPNG(path);
    free(path);
    if (!surface) {
        return NULL;
    }
    SDL_Texture *texture = SDL_CreateTextureFromSurface(renderer, surface);
    SDL_DestroySurface(surface);
    if (texture) {
        SDL_SetTextureBlendMode(texture, SDL_BLENDMODE_BLEND);
        SDL_SetTextureScaleMode(texture, scale);
    }
    return texture;
}

void
sc_calibration_mask_destroy(struct sc_calibration_mask *mask) {
    for (unsigned i = 0; i < 5; ++i) {
        SDL_DestroyTexture(mask->layers[i]);
        mask->layers[i] = NULL;
    }
    SDL_DestroyTexture(mask->caption);
    mask->caption = NULL;
}

bool
sc_calibration_mask_draw(struct sc_calibration_mask *mask, SDL_Renderer *renderer,
                          const SDL_FRect *viewport, sc_tick now) {
    float alpha = sc_calibration_mask_alpha(&mask->state, now);
    if (alpha <= 0 || viewport->w <= 1 || viewport->h <= 1) {
        return false;
    }
    if (!mask->load_attempted) {
        mask->load_attempted = true;
        for (unsigned i = 0; i < 5; ++i) {
            mask->layers[i] = load_art(renderer, sc_calibration_art[i].file,
                                      SDL_SCALEMODE_LINEAR);
        }
        mask->caption = load_art(renderer, "calibration/caption.png",
                                 SDL_SCALEMODE_NEAREST);
        if (!mask->layers[0] || !mask->caption) {
            LOGW("Calibration artwork unavailable: %s", SDL_GetError());
        }
    }

    SDL_BlendMode old_blend;
    Uint8 old_r, old_g, old_b, old_a;
    SDL_GetRenderDrawBlendMode(renderer, &old_blend);
    SDL_GetRenderDrawColor(renderer, &old_r, &old_g, &old_b, &old_a);
    bool covered = SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_BLEND)
                && SDL_SetRenderDrawColor(renderer, 0, 0, 0, (Uint8) lroundf(255 * alpha))
                && SDL_RenderFillRect(renderer, viewport);

    float scale = fminf(viewport->w / 620.f, viewport->h / 680.f);
    float left = viewport->x + (viewport->w - 620 * scale) / 2;
    float top = viewport->y + (viewport->h - 680 * scale) / 2;
    double seconds = (double) (now - mask->state.animation_start)
                   / SC_TICK_FREQ;
    float ring_scale = 270.f / 738.f * scale;
    for (unsigned i = 0; i < 5; ++i) {
        if (!mask->layers[i]) {
            continue;
        }
        const struct sc_calibration_art_layer *art = &sc_calibration_art[i];
        double angle = art->period > 0
            ? art->amplitude * sin(seconds * 2 * 3.141592653589793 / art->period)
            : art->speed * seconds;
        SDL_FPoint pivot = {art->pivot_x * ring_scale,
                            art->pivot_y * ring_scale};
        SDL_FRect dst = {left + 310 * scale - pivot.x,
                        top + 300 * scale - pivot.y,
                        art->width * ring_scale, art->height * ring_scale};
        SDL_SetTextureAlphaModFloat(mask->layers[i], alpha);
        SDL_RenderTextureRotated(renderer, mask->layers[i], NULL, &dst,
                                 angle, &pivot, SDL_FLIP_NONE);
    }
    if (mask->caption) {
        unsigned frame = (unsigned) (seconds * 2) % 3;
        SDL_FRect src = {frame * SC_CALIBRATION_CAPTION_WIDTH, 0,
                        SC_CALIBRATION_CAPTION_WIDTH,
                        SC_CALIBRATION_CAPTION_HEIGHT};
        SDL_FRect dst = {left + SC_CALIBRATION_CAPTION_LEFT * scale,
                        top + 605 * scale, src.w * scale, src.h * scale};
        SDL_SetTextureAlphaModFloat(mask->caption, alpha);
        SDL_RenderTexture(renderer, mask->caption, &src, &dst);
    }
    SDL_SetRenderDrawBlendMode(renderer, old_blend);
    SDL_SetRenderDrawColor(renderer, old_r, old_g, old_b, old_a);
    return covered;
}
