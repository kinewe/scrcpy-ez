#ifndef EZ_GPU_MATTE_H
#define EZ_GPU_MATTE_H
#define COBJMACROS
#include <winsock2.h>
#include <windows.h>
#include <d3d11.h>
#include <stdbool.h>
#include <stdint.h>
#include "badge_layout.h"
struct gpu_matte;
#define EZ_KEY_PLATE_WIDTH 32
#define EZ_KEY_PLATE_HEIGHT 64
#define EZ_KEY_PLATE_SIZE (EZ_KEY_PLATE_WIDTH * EZ_KEY_PLATE_HEIGHT)
struct gpu_matte *gpu_matte_create(ID3D11Device *,unsigned,unsigned);
ID3D11Texture2D *gpu_matte_texture(struct gpu_matte *);
bool gpu_matte_render(struct gpu_matte *,ID3D11ShaderResourceView *,const float inverse[6],const float background[3],double *gpu_us);
void gpu_matte_destroy(struct gpu_matte *);
void gpu_matte_set_badges(struct gpu_matte *,const struct ez_badge *,unsigned);
void gpu_matte_set_camera_region(struct gpu_matte *,const float [4],bool);
void gpu_matte_set_key_levels(struct gpu_matte *,const float [3],unsigned);
void gpu_matte_set_soft_key(struct gpu_matte *);
void gpu_matte_set_key_channel(struct gpu_matte *,unsigned);
void gpu_matte_set_key_plate(struct gpu_matte *,const float [EZ_KEY_PLATE_SIZE]);
bool gpu_matte_set_reference(struct gpu_matte *,const uint8_t *rgba,unsigned pitch);
bool gpu_matte_set_native_reference(struct gpu_matte *,const uint8_t *rgba,unsigned pitch,const float regions[32][4],unsigned count);
bool gpu_matte_set_native_motion(struct gpu_matte *,const uint8_t *rgba,unsigned pitch);
/* Inverse6 generic difference-layer composition. The inverse pipeline learns
 * the keyed video path's own static background during the same warm-up as the
 * native PNG baseline, then adds only the (video - model) dynamic increment on
 * top of native pixels. No color/channel classification: any animated overlay
 * that adds color or alpha behaves the same way. */
#define EZ_GPU_MATTE_INVERSE_OFF 0
#define EZ_GPU_MATTE_INVERSE_LEARN 1
#define EZ_GPU_MATTE_INVERSE_LIVE 2
void gpu_matte_set_inverse_state(struct gpu_matte *,int state);
void gpu_matte_reset_video_model(struct gpu_matte *);
bool gpu_matte_inverse_ready(struct gpu_matte *);
unsigned gpu_matte_inverse_learned_frames(struct gpu_matte *);
#endif
