#ifndef SC_NATIVE_SOURCE_H
#define SC_NATIVE_SOURCE_H
#include "common.h"
#include <stdbool.h>
#include <stdint.h>
#include <SDL3/SDL.h>
/* Lossless RGBA stream and handoff to the compatibility compositor. */
#ifdef _WIN32
bool sc_native_source_start(const char *serial,const char *package);
void sc_native_source_stop(void);
bool sc_native_source_active(void);
void sc_native_source_set_enabled(bool enabled);
void sc_native_source_set_warming(bool warming);
bool sc_native_source_valid(void);
bool sc_native_source_failed(void);
bool sc_native_source_scene_rejected(void);
bool sc_native_source_release(void);
bool sc_native_source_release_pending(void);
enum sc_native_retry {
    SC_NATIVE_RETRY_READY,
    SC_NATIVE_RETRY_PENDING,
    SC_NATIVE_RETRY_FAILED,
};
enum sc_native_retry sc_native_source_retry(void);
uint32_t sc_native_source_scene_epoch(void);
uint32_t sc_native_source_confirmed_scene_epoch(void);
void sc_native_source_scene_covered(uint32_t epoch);
SDL_Surface *sc_native_source_take(uint64_t *generation);
void sc_native_source_ack_event(void);
void sc_native_source_presented(void);
int sc_native_source_fps(void);
#else
static inline bool sc_native_source_start(const char *s,const char *p){(void)s;(void)p;return false;}
static inline void sc_native_source_stop(void){}
static inline bool sc_native_source_active(void){return false;}
#endif
#endif
