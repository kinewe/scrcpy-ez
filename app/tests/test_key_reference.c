#include "key_reference.h"
#include <assert.h>
#include <math.h>
#include <stdio.h>

static unsigned
choose_with_strokes(bool green, bool blue) {
    const uint8_t field[] = {63, 65, 64};
    const uint8_t green_stroke[] = {51, 195, 51};
    const uint8_t blue_stroke[] = {45, 45, 190};
    struct sc_key_probe_samples probe = {0};
    for (int i = 0; i < 4096; ++i) sc_key_probe_sample(&probe, field);
    for (int i = 0; i < 8; ++i) {
        if (green) sc_key_probe_sample(&probe, green_stroke);
        if (blue) sc_key_probe_sample(&probe, blue_stroke);
    }
    assert(sc_key_probe_has_field(&probe));
    float risk[3];
    for (unsigned c = 0; c < 3; ++c) risk[c] = sc_key_probe_risk(&probe, c);
    return sc_key_probe_choose(risk);
}

static void
check_locked_backing(unsigned channel) {
    const unsigned a = (channel + 1) % 3, b = (channel + 2) % 3;
    uint8_t field[3], dimmed[3], codec_shift[3], desktop[3], unmeasured[3];
    field[channel] = channel == 0 ? 87 : 250;
    field[a] = 2;
    field[b] = 3;
    dimmed[channel] = channel == 0 ? 55 : 153;
    dimmed[a] = dimmed[b] = 1;
    codec_shift[channel] = field[channel] - 7;
    codec_shift[a] = 6;
    codec_shift[b] = 8;
    desktop[channel] = field[channel];
    desktop[a] = 40;
    desktop[b] = 55;
    unmeasured[channel] = channel == 0 ? 68 : 200;
    unmeasured[a] = unmeasured[b] = 2;

    struct sc_key_samples samples = {0};
    for (unsigned i = 0; i < 100; ++i) {
        sc_key_sample_for_channel(&samples, field, channel);
        sc_key_sample_for_channel(&samples, dimmed, channel);
    }
    sc_key_sample_for_channel(&samples, unmeasured, channel);
    float reference[3];
    assert(sc_key_reference(&samples, reference));
    struct sc_key_backing backing;
    sc_key_backing_init(&backing, &samples, reference, channel);
    assert(sc_key_matches_backing(&backing, field, channel));
    assert(sc_key_matches_backing(&backing, dimmed, channel));
    assert(sc_key_matches_backing(&backing, codec_shift, channel));
    assert(sc_key_is_backing(desktop, channel));
    assert(!sc_key_matches_backing(&backing, desktop, channel));
    assert(sc_key_is_backing(unmeasured, channel));
    assert(!sc_key_matches_backing(&backing, unmeasured, channel));
}

int main(void) {
    struct sc_key_samples samples={0};float rgb[3]={0};
    const uint8_t dim[3]={0,9,153},key[3]={0,15,255},scan[3]={200,210,255};
    for(int i=0;i<900;++i)sc_key_sample(&samples,dim);
    for(int i=0;i<100;++i)sc_key_sample(&samples,key);
    for(int i=0;i<1000;++i)sc_key_sample(&samples,scan);
    assert(sc_key_reference(&samples,rgb));
    assert(fabs(rgb[2]-1)<.001&&fabs(rgb[1]-15.f/255)<.001&&rgb[0]==0);
    // A native 40% dark overlay must retain its alpha over white desktop.
    float t=(153.f/255)/(rgb[2]-rgb[0]);
    assert(fabs(1-t-.4f)<.001);
    float levels[3];unsigned count=sc_key_flat_levels(&samples,rgb,levels);
    assert(count>=2 && fabs(levels[0]-1)<.001 && fabs(levels[1]-.6f)<.001);
    puts("key reference: undimmed backing selected; scan effect rejected; native dark overlay preserved");

    assert(choose_with_strokes(false, false) == 2);
    assert(choose_with_strokes(true, false) == 2);
    assert(choose_with_strokes(false, true) == 1);
    assert(choose_with_strokes(true, true) == 0);
    struct sc_key_probe_samples stale = {0};
    const uint8_t alignment_field[] = {2, 230, 3};
    const uint8_t blank_frame[] = {0, 0, 0};
    for (int i = 0; i < 100; ++i) {
        sc_key_probe_sample(&stale, alignment_field);
        sc_key_probe_sample(&stale, blank_frame);
    }
    assert(!sc_key_probe_has_field(&stale));
    puts("key probe: neutral and green stay blue, blue selects green, mixed selects red");
    for (unsigned channel = 0; channel < 3; ++channel) check_locked_backing(channel);
    puts("locked backing: rejects same-channel desktops and unmeasured shades, accepts measured dimming and codec shifts");
    return 0;
}
