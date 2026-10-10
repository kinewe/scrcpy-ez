#include "common.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>

#include "image_convert.h"

int main(void) {
    // A 2x2 24-bit BMP, including row padding. This exercises the codecs
    // required by ez's large-BMP clipboard compression with the shipped DLLs.
    const uint8_t bmp[] = {
        'B', 'M', 70, 0, 0, 0, 0, 0, 0, 0, 54, 0, 0, 0,
        40, 0, 0, 0, 2, 0, 0, 0, 2, 0, 0, 0, 1, 0, 24, 0,
        0, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        0, 0, 0, 0, 0, 0, 0, 0,
        0, 0, 255, 0, 255, 0, 0, 0,
        255, 0, 0, 255, 255, 255, 0, 0,
    };
    uint8_t *pixels;
    unsigned width, height;
    assert(sc_image_to_bgra(bmp, sizeof(bmp), "image/bmp",
                            &pixels, &width, &height));
    assert(width == 2 && height == 2);
    free(pixels);

    uint8_t *jpeg;
    size_t size;
    assert(sc_image_bmp_to_jpeg(bmp, sizeof(bmp), &jpeg, &size));
    assert(size > 2 && jpeg[0] == 0xff && jpeg[1] == 0xd8);
    assert(sc_image_to_bgra(jpeg, size, "image/jpeg",
                            &pixels, &width, &height));
    assert(width == 2 && height == 2);
    free(pixels);
    free(jpeg);
    return 0;
}
