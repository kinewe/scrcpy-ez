#ifndef SC_PERSPECTIVE_KEY_H
#define SC_PERSPECTIVE_KEY_H
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static inline bool
sc_perspective_color_key_enabled(void) {
    const char *value = getenv("SCEZ_PERSPECTIVE_COLOR_KEY");
    return value && !strcmp(value, "1");
}

/* Diagnostic override and the default when no usable probe arrives. */
static inline unsigned sc_perspective_key_channel(void) {
    const char *value = getenv("SCEZ_PERSPECTIVE_KEY_COLOR");
    if (value && !strcmp(value, "green")) return 1;
    if (value && !strcmp(value, "red")) return 0;
    return 2;
}

/* .34 * BT.709 red luma (.2126) ~= pure blue luma (.0722). Actual
 * encoded/returned levels still need measurement; this is not a codec claim. */
static inline float sc_perspective_key_strength(unsigned channel) {
    return channel == 0 ? .34f : 1.f;
}

struct sc_key_probe_samples {
    unsigned total, neutral;
    uint64_t energy[3];
};

static inline void
sc_key_probe_sample(struct sc_key_probe_samples *samples, const uint8_t rgb[3]) {
    unsigned low = rgb[0], high = rgb[0];
    for (unsigned c = 1; c < 3; ++c) {
        if (rgb[c] < low) low = rgb[c];
        if (rgb[c] > high) high = rgb[c];
    }
    ++samples->total;
    samples->neutral += low >= 24 && high <= 92 && high - low <= 14;
    // The matte mistakes excess in the backing channel for transmission.
    // Ignore codec noise and give thin saturated strokes more weight.
    for (unsigned c = 0; c < 3; ++c) {
        int excess = (int) rgb[c] - (int) low - 18;
        if (excess > 0) samples->energy[c] += excess * excess;
    }
}

static inline bool
sc_key_probe_has_field(const struct sc_key_probe_samples *samples) {
    return samples->total >= 16 && samples->neutral > samples->total / 2;
}

static inline float
sc_key_probe_risk(const struct sc_key_probe_samples *samples, unsigned channel) {
    float strength = sc_perspective_key_strength(channel);
    return samples->energy[channel] / (samples->total * strength * strength);
}

static inline unsigned
sc_key_probe_choose(const float risk[3]) {
    unsigned best = risk[0] < risk[1] ? 0 : 1;
    // Prefer the established backing when the measured difference is small.
    return risk[best] * 1.25f + .25f < risk[2] ? best : 2;
}

static inline float sc_key_chroma(const float rgb[3], unsigned channel) {
    float a = rgb[(channel + 1) % 3], b = rgb[(channel + 2) % 3];
    return rgb[channel] - (a < b ? a : b);
}

static inline bool sc_key_is_backing(const uint8_t rgb[3], unsigned channel) {
    return rgb[channel] > 32 &&
           (int) rgb[channel] - rgb[(channel + 1) % 3] > 24 &&
           (int) rgb[channel] - rgb[(channel + 2) % 3] > 24;
}
#endif
