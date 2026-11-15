#ifndef SC_NATIVE_UI_H
#define SC_NATIVE_UI_H
#include <SDL3/SDL.h>
#include <windows.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "util/tick.h"
#include "util/log.h"

/* Experimental input from a target-window capture helper.
 * All decoding/resampling is off the rendering thread. Only one newest frame
 * is retained; no queue, recordings or changes to the normal camera source. */
struct sc_native_ui {
    SDL_Thread *thread;SDL_Mutex *mutex;atomic_bool stop;atomic_bool abort_learning;atomic_uint native_changes;SDL_Surface *static_candidate;uint8_t *static_age;
    SDL_Surface *pending;SDL_Texture *texture;
    char path[1024];int w,h;uint64_t seen;
    sc_tick updated;bool valid,shown;
    bool hybrid,inverse;SDL_Surface *pending_model,*latest_model;
    SDL_Surface *pending_motion,*latest_motion;
    float pending_regions[32][4],regions[32][4];unsigned pending_region_count,region_count;
    uint64_t generation;
    /* Inverse6: one epoch per warm-up learned model (reset on mode/layout
     * change) and the phase of that model: 0 learning, 1 frozen envelope.
     * Published with the newest frame, read on the render thread only. */
    unsigned pending_inverse_epoch,inverse_epoch;
    unsigned pending_inverse_phase,inverse_phase;
    /* Calibrated camera-preview rectangle (normalized video coordinates). The
     * inverse learner cleans exactly this region, so a light that moves past the
     * 10 s warm-up range cannot leave a stale native imprint at an envelope edge.
     * Guarded by ui->mutex. */
    float camera_region[4];bool camera_region_valid;
    /* RC.20: explicit file paths and adaptive warm-up parameters (set by
     * sc_native_ui_init_ex; the env-based init wrapper keeps research/tests). */
    char regions_path[1024];
    unsigned warmup_ms, warmup_stable_ms, warmup_min_frames;
};
static inline unsigned sc_native_ui_regions_from(const char *path,int w,int h,float regions[32][4]) {
    if(!path||!*path)return 0;
    FILE *file=fopen(path,"rb");if(!file)return 0;
    char magic[4];uint32_t header[3];unsigned count=0;
    if(fread(magic,1,4,file)!=4||memcmp(magic,"EVS1",4)||fread(header,4,3,file)!=3)goto done;
    if(!header[0]||!header[1]||header[2]>32)goto done;
    double aspect=(double)header[0]*h/((double)header[1]*w);if(aspect<.98||aspect>1.02)goto done;
    for(unsigned i=0;i<header[2];++i){int32_t rect[4];if(fread(rect,4,4,file)!=4){count=0;goto done;}
        if(rect[0]<0||rect[1]<0||rect[2]<=0||rect[3]<=0||
           (int64_t)rect[0]+rect[2]>header[0]||(int64_t)rect[1]+rect[3]>header[1]){count=0;goto done;}
        regions[count][0]=(float)rect[0]/header[0];regions[count][1]=(float)rect[1]/header[1];
        regions[count][2]=(float)rect[2]/header[0];regions[count][3]=(float)rect[3]/header[1];++count;
    }
done:fclose(file);return count;
}
static inline unsigned sc_native_ui_regions(int w,int h,float regions[32][4]) {
    return sc_native_ui_regions_from(getenv("SCEZ_NATIVE_UI_REGIONS_FILE"),w,h,regions);
}
static inline uint64_t sc_native_ui_region_stamp_at(const char *path) {
    WIN32_FILE_ATTRIBUTE_DATA a;if(!path||!*path)return 0;
    return path&&GetFileAttributesExA(path,GetFileExInfoStandard,&a)
        ?((uint64_t)a.ftLastWriteTime.dwHighDateTime<<32)|a.ftLastWriteTime.dwLowDateTime:0;
}
static inline uint64_t sc_native_ui_region_stamp(void) {
    return sc_native_ui_region_stamp_at(getenv("SCEZ_NATIVE_UI_REGIONS_FILE"));
}
/* Preserve the latest native pixels for View components. For the remainder,
 * retain the least opaque sample to remove moving light from the baseline. */
static inline void sc_native_ui_model(SDL_Surface *base,const SDL_Surface *frame,const float regions[32][4],unsigned count,bool first) {
    for(int y=0;y<frame->h;++y){const uint8_t *p=(const uint8_t*)frame->pixels+y*frame->pitch;uint8_t *q=(uint8_t*)base->pixels+y*base->pitch;
        for(int x=0;x<frame->w;++x,p+=4,q+=4){bool component=false;float u=(x+.5f)/frame->w,v=(y+.5f)/frame->h;
            for(unsigned i=0;i<count;++i)if(u>=regions[i][0]&&v>=regions[i][1]&&u<regions[i][0]+regions[i][2]&&v<regions[i][1]+regions[i][3]){component=true;break;}
            unsigned light=p[0]+p[1]+p[2],old=first?0:q[0]+q[1]+q[2];
            if(first||component||p[3]<q[3]||(p[3]==q[3]&&light<old))memcpy(q,p,4);
        }
    }
}
/* Learn a bounded sweep before publishing any video support. After warm-up,
 * keep the support fixed, and keep removing animated light from the baseline. */
static inline void sc_native_ui_inverse_model(SDL_Surface *base,SDL_Surface *motion,const SDL_Surface *frame,const float regions[32][4],unsigned count,bool first,bool learning) {
    for(int y=0;y<frame->h;++y){const uint8_t *p=(const uint8_t*)frame->pixels+y*frame->pitch;
        uint8_t *q=(uint8_t*)base->pixels+y*base->pitch,*mask=(uint8_t*)motion->pixels+y*motion->pitch;
        for(int x=0;x<frame->w;++x,p+=4,q+=4,mask+=4){bool component=false;
            float u=(x+.5f)/frame->w,v=(y+.5f)/frame->h;
            for(unsigned i=0;i<count;++i)if(u>=regions[i][0]&&v>=regions[i][1]&&u<regions[i][0]+regions[i][2]&&v<regions[i][1]+regions[i][3]){component=true;break;}
            if(component){
                /* Protected component pixels are rendered from the learned base, so they
                 * must still be cleaned: a frame captured while the sweep light passed
                 * over the component would otherwise freeze that glow into the base and
                 * make the persistent "ghost" the user sees next to static UI. Keep the
                 * mask clear (compose falls back to the cleaned native base there). */
                if(!first&&(p[3]<q[3]||(p[3]==q[3]&&p[0]+p[1]+p[2]<q[0]+q[1]+q[2])))memcpy(q,p,4);
                memset(mask,0,4);continue;
            }
            if(first){memcpy(q,p,4);continue;}
            int delta=0;for(unsigned c=0;c<4;++c){int d=abs((int)p[c]-q[c]);if(d>delta)delta=d;}
            /* The native View capture is exact rendering: two frames of unchanged pixels are
             * identical, so a low threshold costs no false motion. 8/255 missed the faint
             * upper halo (2-3% alpha) that the user saw as periodic residue above the
             * cleaned envelope. 2/255 includes it in the learned base/sway support. */
            if(learning&&delta>2)mask[0]=255;
            if(!mask[0]||p[3]<q[3]||(p[3]==q[3]&&p[0]+p[1]+p[2]<q[0]+q[1]+q[2]))memcpy(q,p,4);
        }
    }
}
/* A sweep sampled at 2fps has holes. Fill its envelope once, rather than
 * exposing a growing, scalloped union to the renderer throughout the session. */
static inline bool sc_native_ui_inverse_freeze(SDL_Surface *motion,const float regions[32][4],unsigned count) {
    int left=motion->w,top=motion->h,right=0,bottom=0;
    for(int y=0;y<motion->h;++y){const uint8_t *p=(const uint8_t*)motion->pixels+y*motion->pitch;
        for(int x=0;x<motion->w;++x,p+=4)if(p[0]){if(x<left)left=x;if(y<top)top=y;if(x+1>right)right=x+1;if(y+1>bottom)bottom=y+1;}}
    if(left>=right||top>=bottom)return false;
    if(left>2)left-=2;else left=0;if(top>2)top-=2;else top=0;
    right=right+2<motion->w?right+2:motion->w;bottom=bottom+2<motion->h?bottom+2:motion->h;
    for(int y=0;y<motion->h;++y){uint8_t *p=(uint8_t*)motion->pixels+y*motion->pitch;
        for(int x=0;x<motion->w;++x,p+=4){bool inside=x>=left&&x<right&&y>=top&&y<bottom;
            float u=(x+.5f)/motion->w,v=(y+.5f)/motion->h;
            for(unsigned i=0;i<count&&inside;++i)if(u>=regions[i][0]&&v>=regions[i][1]&&u<regions[i][0]+regions[i][2]&&v<regions[i][1]+regions[i][3])inside=false;
            memset(p,0,4);if(inside)p[0]=255;
        }
    }
    return true;
}
static inline void sc_native_ui_set_camera_region(struct sc_native_ui *ui,const float region[4],bool valid){
    if(!ui||!ui->mutex)return;
    SDL_LockMutex(ui->mutex);
    if(valid)memcpy(ui->camera_region,region,sizeof(ui->camera_region));
    ui->camera_region_valid=valid;
    SDL_UnlockMutex(ui->mutex);
}
/* Fill the inverse cleaning mask with the camera-preview rectangle (plus a small
 * margin for calibration fringes), excluding protected component rectangles. */
static inline void sc_native_ui_inverse_region_mask(SDL_Surface *mask,const float region[4],const float regions[32][4],unsigned count) {
    const float margin=.012f;
    float u0=region[0]-margin,v0=region[1]-margin,u1=region[0]+region[2]+margin,v1=region[1]+region[3]+margin;
    if(u0<0)u0=0;if(v0<0)v0=0;if(u1>1)u1=1;if(v1>1)v1=1;
    for(int y=0;y<mask->h;++y){uint8_t *q=(uint8_t*)mask->pixels+y*mask->pitch;
        float v=(y+.5f)/mask->h;
        for(int x=0;x<mask->w;++x,q+=4){float u=(x+.5f)/mask->w;
            bool inside=u>=u0&&u<u1&&v>=v0&&v<v1;
            for(unsigned i=0;i<count&&inside;++i)if(u>=regions[i][0]&&v>=regions[i][1]&&u<regions[i][0]+regions[i][2]&&v<regions[i][1]+regions[i][3])inside=false;
            memset(q,0,4);if(inside)q[0]=255;
        }
    }
}
static inline bool sc_native_ui_header(const char *path,int w,int h) {
    unsigned char p[24];FILE *file=fopen(path,"rb");if(!file)return false;
    size_t n=fread(p,1,sizeof(p),file);fclose(file);
    if(n!=24||memcmp(p,"\x89PNG\r\n\x1a\n",8)||memcmp(p+12,"IHDR",4))return false;
    uint32_t iw=((uint32_t)p[16]<<24)|((uint32_t)p[17]<<16)|((uint32_t)p[18]<<8)|p[19];
    uint32_t ih=((uint32_t)p[20]<<24)|((uint32_t)p[21]<<16)|((uint32_t)p[22]<<8)|p[23];
    if(!iw||!ih||(uint64_t)iw*ih>16777216)return false;
    double ratio=(double)iw*h/((double)ih*w);
    return ratio>.98&&ratio<1.02;
}
static inline SDL_Surface *sc_native_ui_decode(const char *path,int w,int h) {
    if(!sc_native_ui_header(path,w,h))return NULL;
    SDL_Surface *loaded=SDL_LoadPNG(path);if(!loaded)return NULL;
    SDL_Surface *rgba=SDL_ConvertSurface(loaded,SDL_PIXELFORMAT_RGBA32);SDL_DestroySurface(loaded);if(!rgba)return NULL;
    size_t transparent=0,total=(size_t)rgba->w*rgba->h;
    for(int y=0;y<rgba->h;++y){uint8_t *p=(uint8_t*)rgba->pixels+y*rgba->pitch;
        for(int x=0;x<rgba->w;++x,p+=4){unsigned a=p[3];if(a<250)++transparent;
            p[0]=(uint8_t)((p[0]*a+127)/255);p[1]=(uint8_t)((p[1]*a+127)/255);p[2]=(uint8_t)((p[2]*a+127)/255);}}
    // RGBA alone is insufficient: reject flattened opaque captures.
    if(transparent<total/200){SDL_DestroySurface(rgba);return NULL;}
    SDL_Surface *scaled=SDL_CreateSurface(w,h,SDL_PIXELFORMAT_RGBA32);
    SDL_SetSurfaceBlendMode(rgba,SDL_BLENDMODE_NONE);
    if(!scaled||!SDL_BlitSurfaceScaled(rgba,NULL,scaled,NULL,SDL_SCALEMODE_LINEAR)){SDL_DestroySurface(scaled);scaled=NULL;}
    SDL_DestroySurface(rgba);return scaled;
}
/* RC.22 continuous static learner: a pixel value that stays different from the
 * learned base for RC22_STATIC_PERSIST consecutive native frames is adopted into
 * the base. Moving scan light/effects are transient and never persist that long,
 * while new/changed opaque UI (late text, tab selection, dialogs) does, so the
 * static layer keeps updating after the initial freeze. */
#define RC22_STATIC_PERSIST 4
static void sc_native_ui_learn_static(struct sc_native_ui *ui, SDL_Surface *base,
                                      const SDL_Surface *frame) {
    if (!ui->static_candidate || !ui->static_age) {
        return;
    }
    unsigned adopted = 0;
    for (int y = 0; y < frame->h; ++y) {
        const uint8_t *f = (const uint8_t *) frame->pixels + y * frame->pitch;
        uint8_t *b = (uint8_t *) base->pixels + y * base->pitch;
        uint8_t *c = (uint8_t *) ui->static_candidate->pixels
                   + y * ui->static_candidate->pitch;
        uint8_t *age = ui->static_age + y * base->pitch / 4;
        for (int x = 0; x < frame->w; ++x, f += 4, b += 4, c += 4) {
            int diff = abs((int) f[0] - (int) b[0]);
            if (abs((int) f[1] - (int) b[1]) > diff) diff = abs((int) f[1] - (int) b[1]);
            if (abs((int) f[2] - (int) b[2]) > diff) diff = abs((int) f[2] - (int) b[2]);
            if (abs((int) f[3] - (int) b[3]) > diff) diff = abs((int) f[3] - (int) b[3]);
            if (diff > 20) {
                int cdiff = memcmp(f, c, 4) != 0;
                if (cdiff || age[x] == 0) {
                    memcpy(c, f, 4);
                    age[x] = 1;
                } else if (age[x] < RC22_STATIC_PERSIST) {
                    ++age[x];
                }
                if (age[x] >= RC22_STATIC_PERSIST) {
                    memcpy(b, f, 4);
                    age[x] = 0;
                    ++adopted;
                }
            } else {
                age[x] = 0;
            }
        }
    }
    if (adopted) {
        /* One line per update; content changes are visible to the model
         * publisher in the same worker loop. */
        static unsigned updates;
        if ((++updates % 20) == 1) {
            LOGI("NATIVE_UI static layer updated: adopted %u px (frame %u)",
                 adopted, updates);
        }
    }
}
static int sc_native_ui_worker(void *data) {
    { struct sc_native_ui *u=data; LOGI("NATIVE_UI worker started: %dx%d hybrid=%d inverse=%d",u->w,u->h,(int)u->hybrid,(int)u->inverse); }
    struct sc_native_ui *ui=data;SDL_Surface *base=NULL,*motion=NULL;
    sc_tick learning_started=0;unsigned learning_frames=0;bool frozen=false;
    sc_tick last_motion_growth=0;int box_left=-1,box_top=-1,box_right=-1,box_bottom=-1;
    unsigned refined_frames=0;
    unsigned learning_epoch=0;
    float learned_regions[32][4];unsigned learned_count=0;
    uint64_t learned_stamp=0;
    float regions[32][4];unsigned count=0;
    float applied_region[4];bool have_applied_region=false;
    while(!atomic_load(&ui->stop)){
        if(atomic_load(&ui->abort_learning)){
            atomic_store(&ui->abort_learning,false);
            SDL_DestroySurface(base);base=NULL;SDL_DestroySurface(motion);motion=NULL;
            learning_started=0;learning_frames=0;frozen=false;learned_count=0;
            have_applied_region=false;refined_frames=0;
            box_left=box_top=box_right=box_bottom=-1;last_motion_growth=0;
            ++learning_epoch;
            SDL_LockMutex(ui->mutex);
            SDL_DestroySurface(ui->pending);ui->pending=NULL;
            SDL_DestroySurface(ui->pending_model);ui->pending_model=NULL;
            SDL_DestroySurface(ui->pending_motion);ui->pending_motion=NULL;
            ui->pending_inverse_epoch=learning_epoch;ui->pending_inverse_phase=0;
            ui->valid=false;SDL_UnlockMutex(ui->mutex);
            LOGI("NATIVE_UI inverse learning aborted (camera closed or page changed)");
        }
        float camera_region[4];bool camera_region_valid;
        SDL_LockMutex(ui->mutex);memcpy(camera_region,ui->camera_region,sizeof(camera_region));camera_region_valid=ui->camera_region_valid;SDL_UnlockMutex(ui->mutex);
        bool region_changed=base&&ui->inverse&&(camera_region_valid!=have_applied_region||(camera_region_valid&&memcmp(camera_region,applied_region,sizeof(camera_region))));
        if(region_changed){
            /* Re-apply the cleaning region to the existing learning instead of
             * restarting it: the learned base does not depend on the mask, and a
             * restart would cost another full warm-up. */
            if(camera_region_valid){
                sc_native_ui_inverse_region_mask(motion,camera_region,NULL,0);
                memcpy(applied_region,camera_region,sizeof(applied_region));have_applied_region=true;
            }else{
                have_applied_region=false;
            }
            box_left=box_top=box_right=box_bottom=-1;last_motion_growth=0;
            LOGI("NATIVE_UI inverse camera preview region updated; applied to the running learning");
        }
        WIN32_FILE_ATTRIBUTE_DATA a;bool exists=GetFileAttributesExA(ui->path,GetFileExInfoStandard,&a)!=0;
        uint64_t stamp=exists?((uint64_t)a.ftLastWriteTime.dwHighDateTime<<32)|a.ftLastWriteTime.dwLowDateTime:0;
        if(!exists){SDL_LockMutex(ui->mutex);ui->valid=false;SDL_UnlockMutex(ui->mutex);ui->seen=0;SDL_DestroySurface(base);base=NULL;SDL_DestroySurface(motion);motion=NULL;learning_started=0;learning_frames=0;frozen=false;learned_count=0;}
        else if(stamp!=ui->seen){
            ui->seen=stamp;
            SDL_Surface *frame=!a.nFileSizeHigh&&a.nFileSizeLow<=24*1024*1024?sc_native_ui_decode(ui->path,ui->w,ui->h):NULL;
            if(!frame){
                static sc_tick last_decode_log;
                sc_tick nowlog=sc_tick_now();
                if(nowlog-last_decode_log>SC_TICK_FROM_MS(2000)){
                    last_decode_log=nowlog;
                    LOGW("NATIVE_UI native frame decode failed: size=%lu path=%s",(unsigned long)a.nFileSizeLow,ui->path);
                }
            }
            SDL_Surface *model=NULL,*motion_copy=NULL;
            if(frame&&ui->hybrid){
                /* Start the warm-up as soon as the first native frame arrives: the
                 * protected-region file is only needed to protect component rects, so
                 * learning starts in envelope mode and region metadata is applied to the
                 * running learning when it lands (no restart). */
                count=sc_native_ui_regions_from(ui->regions_path,ui->w,ui->h,regions);
                uint64_t region_stamp=count?sc_native_ui_region_stamp_at(ui->regions_path):0;
                /* Restart only when the protected-region CONTENT changes. The probe
                 * re-emits identical layouts periodically, which bumps the file mtime;
                 * restarting on that would prevent the warm-up from ever completing. */
                if(count&&learned_count&&ui->inverse&&base&&(count!=learned_count||memcmp(regions,learned_regions,count*sizeof(regions[0])))){
                    SDL_DestroySurface(base);base=NULL;SDL_DestroySurface(motion);motion=NULL;
                    learning_started=0;learning_frames=0;frozen=false;
                }
                bool first=base==NULL;if(first)base=SDL_CreateSurface(ui->w,ui->h,SDL_PIXELFORMAT_RGBA32);
                if(base){
                    if(ui->inverse){
                        if(!motion)motion=SDL_CreateSurface(ui->w,ui->h,SDL_PIXELFORMAT_RGBA32);
                        if(motion){
                            if(first){learning_started=sc_tick_now();learning_frames=0;frozen=false;
                                ++learning_epoch;
                                learned_count=count;if(count)memcpy(learned_regions,regions,count*sizeof(regions[0]));
                                learned_stamp=region_stamp;LOGI("NATIVE_UI inverse learning: component regions=%u",count);
                                LOGI("NATIVE_UI inverse learning: native PNG only during adaptive warm-up %ums (epoch %u, camera region %d)",ui->warmup_ms,learning_epoch,camera_region_valid?1:0);}
                            if(frozen&&!first){
                                /* Continuous static learning after the freeze: adopt
                                 * persistent per-pixel changes (see helper above). */
                                if(!ui->static_candidate){
                                    ui->static_candidate=SDL_CreateSurface(ui->w,ui->h,SDL_PIXELFORMAT_RGBA32);
                                    ui->static_age=calloc((size_t)ui->w*ui->h,1);
                                }
                                sc_native_ui_learn_static(ui,base,frame);
                            }
                            sc_native_ui_inverse_model(base,motion,frame,NULL,0,first,!frozen);
                            /* The model call zeroes the whole mask on its first frame, so
                             * fill the calibrated camera-preview cleaning region afterwards:
                             * the region must survive as the steady cleaning mask, with
                             * motion detection only adding extra coverage. */
                            if(first&&camera_region_valid){sc_native_ui_inverse_region_mask(motion,camera_region,NULL,0);
                                memcpy(applied_region,camera_region,sizeof(applied_region));have_applied_region=true;}
                            ++learning_frames;
                            sc_tick now=sc_tick_now();
                            // Track sweep support growth: with the calibrated camera
                            // region the mask is already complete, otherwise freeze only
                            // once the observed motion envelope stops growing.
                            int ml=ui->w,mt=ui->h,mr=-1,mb=-1;
                            for(int yy=0;yy<motion->h;++yy){uint8_t *row=(uint8_t*)motion->pixels+yy*motion->pitch;
                                for(int xx=0;xx<motion->w;++xx,row+=4)if(row[0]){
                                    if(xx<ml)ml=xx;if(xx>mr)mr=xx;if(yy<mt)mt=yy;if(yy>mb)mb=yy;}}
                            if(mr>=0&&(box_right<0||ml<box_left||mt<box_top||mr>box_right||mb>box_bottom)){
                                box_left=ml;box_top=mt;box_right=mr;box_bottom=mb;last_motion_growth=now;}
                            bool elapsed_ok=now-learning_started>=SC_TICK_FROM_MS(ui->warmup_ms);
                            bool enough_frames=learning_frames>=ui->warmup_min_frames;
                            bool support_stable=have_applied_region
                                ||(last_motion_growth&&now-last_motion_growth>=SC_TICK_FROM_MS(ui->warmup_stable_ms));
                            bool warmup_done=enough_frames&&elapsed_ok&&support_stable;
                            bool warmup_cap=learning_frames>=16&&now-learning_started>=SC_TICK_FROM_MS(10000);
                            if(!frozen&&(warmup_done||warmup_cap)){
                                bool ok=have_applied_region;
                                if(!ok)ok=sc_native_ui_inverse_freeze(motion,NULL,0);
                                if(ok){frozen=true;
                                    LOGI("NATIVE_UI inverse support frozen after %ums/%u frames (%s): %s",
                                        (unsigned)SC_TICK_TO_MS(now-learning_started),learning_frames,
                                        warmup_done?"adaptive":"hard cap",
                                        have_applied_region?"whole calibrated camera preview (no envelope edge)":"learned motion envelope (no camera region yet)");
                                }
                            }
                            if(frozen){
                                /* RC.21 fast-start: the freeze is deliberately early (few
                                 * frames). Later native frames still refine the static base
                                 * through the min-alpha rule (a lower-alpha sample only ever
                                 * removes light/halo), so publish the improved model every
                                 * frame; the client flushes it to the GPU on each generation
                                 * change. Log every 30 refined frames so convergence is
                                 * visible in the live log. */
                                ++refined_frames;
                                if(refined_frames%30==0){
                                    LOGI("NATIVE_UI inverse base refined frame=%u epoch=%u",refined_frames,learning_epoch);
                                }
                                model=SDL_DuplicateSurface(base);motion_copy=SDL_DuplicateSurface(motion);}
                        }
                        else{SDL_DestroySurface(base);base=NULL;}
                    }else if(count){sc_native_ui_model(base,frame,regions,count,first);model=SDL_DuplicateSurface(base);}
                    else{SDL_DestroySurface(base);base=NULL;}
                }
            }else{SDL_DestroySurface(base);base=NULL;SDL_DestroySurface(motion);motion=NULL;learning_started=0;learning_frames=0;frozen=false;learned_count=0;}
            SDL_LockMutex(ui->mutex);SDL_DestroySurface(ui->pending);SDL_DestroySurface(ui->pending_model);SDL_DestroySurface(ui->pending_motion);
            ui->pending=frame;ui->pending_model=model;ui->pending_motion=motion_copy;ui->pending_region_count=count;
            ui->pending_inverse_epoch=learning_epoch;ui->pending_inverse_phase=frozen?1:0;
            if(count)memcpy(ui->pending_regions,regions,count*sizeof(regions[0]));ui->valid=frame!=NULL;
            if(frame)ui->updated=sc_tick_now();SDL_UnlockMutex(ui->mutex);
        }
        SDL_Delay(40);
    }
    SDL_DestroySurface(base);SDL_DestroySurface(motion);
    return 0;
}
/* RC.22: stop the background dual-channel learning (camera closed or the
 * scanner page changed). The worker drops its learned base and bumps the epoch
 * so a later re-entry starts a fresh learning session. */
static inline void sc_native_ui_abort(struct sc_native_ui *ui) {
    if (ui->thread) {
        atomic_store(&ui->abort_learning, true);
    }
}
static inline void sc_native_ui_destroy(struct sc_native_ui *ui) {
    if(ui->thread){atomic_store(&ui->stop,true);SDL_WaitThread(ui->thread,NULL);}
    SDL_DestroySurface(ui->static_candidate);ui->static_candidate=NULL;free(ui->static_age);ui->static_age=NULL;
    SDL_DestroySurface(ui->pending);SDL_DestroySurface(ui->pending_model);SDL_DestroySurface(ui->latest_model);
    SDL_DestroySurface(ui->pending_motion);SDL_DestroySurface(ui->latest_motion);
    SDL_DestroyTexture(ui->texture);SDL_DestroyMutex(ui->mutex);memset(ui,0,sizeof(*ui));
}
static inline unsigned sc_native_ui_env_uint(const char *name,unsigned fallback,unsigned lo,unsigned hi) {
    const char *value=getenv(name);if(!value||!*value)return fallback;
    char *end;unsigned long parsed=strtoul(value,&end,10);
    if(*end||parsed<lo||parsed>hi)return fallback;return (unsigned)parsed;
}
static inline void sc_native_ui_init_ex(struct sc_native_ui *ui,int w,int h,const char *frame_path,
                                        const char *regions_path,bool hybrid,bool inverse) {
    if(!frame_path||!*frame_path||strlen(frame_path)>=sizeof(ui->path))return;
    strcpy(ui->path,frame_path);
    if(regions_path&&*regions_path&&strlen(regions_path)<sizeof(ui->regions_path))strcpy(ui->regions_path,regions_path);
    ui->w=w;ui->h=h;atomic_init(&ui->stop,false);atomic_init(&ui->abort_learning,false);atomic_init(&ui->native_changes,0);ui->mutex=SDL_CreateMutex();
    ui->hybrid=hybrid;ui->inverse=inverse;
    ui->warmup_ms=sc_native_ui_env_uint("SCEZ_NATIVE_UI_WARMUP_MS",1200,300,10000);
    ui->warmup_stable_ms=sc_native_ui_env_uint("SCEZ_NATIVE_UI_WARMUP_STABLE_MS",300,100,5000);
    ui->warmup_min_frames=sc_native_ui_env_uint("SCEZ_NATIVE_UI_WARMUP_FRAMES",4,2,120);
    if(!ui->mutex)return;
    ui->thread=SDL_CreateThread(sc_native_ui_worker,"native-ui-alpha",ui);
    if(!ui->thread){sc_native_ui_destroy(ui);return;}
    LOGI("NATIVE_UI optional original-RGBA input enabled (hybrid=%d inverse=%d auto=%d); automatic single-key fallback active",
         (int)ui->hybrid,(int)ui->inverse,(int)(frame_path&&!getenv("SCEZ_NATIVE_UI_FRAME_FILE")));
}
static inline void sc_native_ui_init(struct sc_native_ui *ui,int w,int h) {
    const char *path=getenv("SCEZ_NATIVE_UI_FRAME_FILE");
    const char *regions=getenv("SCEZ_NATIVE_UI_REGIONS_FILE");
    const char *hybrid=getenv("SCEZ_NATIVE_UI_HYBRID");bool hybrid_on=hybrid&&!strcmp(hybrid,"1");
    const char *inverse=getenv("SCEZ_NATIVE_UI_INVERSE");bool inverse_on=inverse&&!strcmp(inverse,"1");
    sc_native_ui_init_ex(ui,w,h,path,regions,hybrid_on,inverse_on);
}
static inline SDL_Texture *sc_native_ui_pick(struct sc_native_ui *ui,SDL_Renderer *renderer,sc_tick now) {
    if(!ui->thread)return NULL;
    SDL_Surface *frame=NULL,*model=NULL,*motion=NULL;bool valid;sc_tick updated;
    SDL_LockMutex(ui->mutex);frame=ui->pending;ui->pending=NULL;model=ui->pending_model;ui->pending_model=NULL;
    motion=ui->pending_motion;ui->pending_motion=NULL;
    if(frame){ui->region_count=ui->pending_region_count;memcpy(ui->regions,ui->pending_regions,sizeof(ui->regions));
        ui->inverse_epoch=ui->pending_inverse_epoch;ui->inverse_phase=ui->pending_inverse_phase;}
    valid=ui->valid;updated=ui->updated;SDL_UnlockMutex(ui->mutex);
    if(frame){
        if(!ui->texture){ui->texture=SDL_CreateTexture(renderer,SDL_PIXELFORMAT_RGBA32,SDL_TEXTUREACCESS_STREAMING,ui->w,ui->h);
            if(ui->texture)SDL_SetTextureBlendMode(ui->texture,SDL_BLENDMODE_NONE);}
        if(!ui->texture||!SDL_UpdateTexture(ui->texture,NULL,frame->pixels,frame->pitch)){valid=false;SDL_LockMutex(ui->mutex);ui->valid=false;SDL_UnlockMutex(ui->mutex);}
        SDL_DestroySurface(frame);
        SDL_DestroySurface(ui->latest_model);ui->latest_model=model;++ui->generation;
        SDL_DestroySurface(ui->latest_motion);ui->latest_motion=motion;
    }else{SDL_DestroySurface(model);SDL_DestroySurface(motion);}
    if(!valid){SDL_DestroySurface(ui->latest_model);ui->latest_model=NULL;ui->region_count=0;
        SDL_DestroySurface(ui->latest_motion);ui->latest_motion=NULL;
    }
    bool show=valid&&ui->texture&&updated&&now-updated<SC_TICK_FROM_MS(5000);
    if(show!=ui->shown){LOGI("NATIVE_UI source=%s",show?"original-alpha":"single-key-fallback");ui->shown=show;}
    return show?ui->texture:NULL;
}
#endif
