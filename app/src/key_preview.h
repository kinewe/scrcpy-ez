#ifndef SC_KEY_PREVIEW_H
#define SC_KEY_PREVIEW_H

#include "local_preview.h"

SDL_Texture *sc_key_preview_texture(SDL_Renderer *renderer,
                                   struct sc_perspective *perspective,
                                   const AVFrame *frame, SDL_Texture *fallback);
bool sc_key_preview_is_composited(SDL_Texture *texture);
void sc_key_preview_destroy(void);

#endif
