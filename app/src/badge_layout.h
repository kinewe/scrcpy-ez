#ifndef EZ_BADGE_LAYOUT_H
#define EZ_BADGE_LAYOUT_H
#include <stdbool.h>
#include <stdint.h>
#define EZ_MAX_BADGES 8
/* Two float4 registers per sprite, shared verbatim with the shader. */
struct ez_badge {float src_x,src_y,dst_x,dst_y,radius,reserved[3];};
struct ez_geometry {double x,y,w,h;};
unsigned ez_badge_detect(const uint8_t *,int,int,int,struct ez_badge *,unsigned);
bool ez_badge_world(const double map[6],double u,double v,const struct ez_geometry *,double out[2]);
bool ez_badge_hit(const struct ez_badge *,unsigned,float,float,float *,float *);
#endif
