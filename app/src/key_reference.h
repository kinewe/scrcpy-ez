#ifndef SC_KEY_REFERENCE_H
#define SC_KEY_REFERENCE_H
#include <math.h>
#include <stdint.h>
#include <stdbool.h>
#include "perspective_key.h"
struct sc_key_samples { unsigned count[32], sum[32][3], total; };
static inline void sc_key_sample_for_channel(struct sc_key_samples *samples, const uint8_t rgb[3], unsigned channel) {
    // Sample the saturated backing field, including app-dimmed regions.
    if (rgb[channel]<32 || rgb[(channel+1)%3]*6>rgb[channel] || rgb[(channel+2)%3]*6>rgb[channel]) return;
    unsigned bin=rgb[channel]/8;
    ++samples->count[bin];++samples->total;
    for (int c=0;c<3;++c) samples->sum[bin][c]+=rgb[c];
}
static inline void sc_key_sample(struct sc_key_samples *samples, const uint8_t rgb[3]) {
    sc_key_sample_for_channel(samples,rgb,2);
}
static inline bool sc_key_reference(const struct sc_key_samples *samples, float rgb[3]) {
    unsigned minimum=samples->total/100;
    if (minimum<2) minimum=2;
    for (int bin=31;bin>=0;--bin) {
        if (samples->count[bin]<minimum) continue;
        for (int c=0;c<3;++c) rgb[c]=(float)samples->sum[bin][c]/samples->count[bin]/255.f;
        return true;
    }
    return false;
}

struct sc_key_backing {
    uint32_t bands;
    float other_scale[2];
};

static inline void
sc_key_backing_init(struct sc_key_backing *backing, const struct sc_key_samples *samples,
                    const float reference[3], unsigned channel) {
    unsigned minimum = samples->total / 100;
    if (minimum < 2) minimum = 2;
    uint32_t bands = 0;
    for (unsigned i = 0; i < 32; ++i) {
        if (samples->count[i] >= minimum) bands |= UINT32_C(1) << i;
    }
    // Permit codec variation around the observed brightness bands. Keep the
    // gaps between them so a different solid shade cannot join the reference.
    backing->bands = bands | (bands << 1) | (bands >> 1);
    backing->other_scale[0] = reference[(channel + 1) % 3] / reference[channel];
    backing->other_scale[1] = reference[(channel + 2) % 3] / reference[channel];
}

static inline bool
sc_key_matches_backing(const struct sc_key_backing *backing, const uint8_t rgb[3],
                       unsigned channel) {
    unsigned strength = rgb[channel];
    return (backing->bands & (UINT32_C(1) << (strength / 8)))
        && sc_key_is_backing(rgb, channel)
        && fabsf(rgb[(channel + 1) % 3] - strength * backing->other_scale[0]) <= 10
        && fabsf(rgb[(channel + 2) % 3] - strength * backing->other_scale[1]) <= 10;
}

static inline unsigned sc_key_flat_levels_for_channel(const struct sc_key_samples *samples, const float reference[3], float levels[3], unsigned channel) {
    levels[0]=1.f;unsigned count=1;
    float denominator=sc_key_chroma(reference,channel);
    if(denominator<.1f)return count;
    unsigned used=0;
    for(int pass=0;pass<32&&count<3;++pass){
        int best=-1;unsigned population=0;
        for(unsigned i=0;i<32;++i)if(!(used&(1u<<i))&&samples->count[i]>population){best=i;population=samples->count[i];}
        if(best<0||population<samples->total/100||population<4)break;
        used|=1u<<best;
        float rgb[3];for(int c=0;c<3;++c)rgb[c]=(float)samples->sum[best][c]/population/255.f;
        float level=sc_key_chroma(rgb,channel)/denominator;
        bool separated=true;for(unsigned i=0;i<count;++i)if(level>levels[i]-.08f&&level<levels[i]+.08f)separated=false;
        if(separated&&level>.08f&&level<.94f)levels[count++]=level;
    }
    return count;
}
static inline unsigned sc_key_flat_levels(const struct sc_key_samples *samples, const float reference[3], float levels[3]) {
    return sc_key_flat_levels_for_channel(samples,reference,levels,2);
}
#endif
