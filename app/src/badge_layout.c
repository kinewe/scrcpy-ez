#include "badge_layout.h"
#include <stdlib.h>
#include <math.h>
unsigned ez_badge_detect(const uint8_t *rgba,int w,int h,int stride,struct ez_badge *out,unsigned cap){
 if(w<32||h<32||stride<w*4||cap>EZ_MAX_BADGES)return 0;
 const int step=4,sw=(w+step-1)/step,sh=(h+step-1)/step;
 size_t n=(size_t)sw*sh;if(n>4000000)return 0;
 uint8_t *mask=calloc(n,1);unsigned *queue=malloc(n*sizeof(*queue));
 if(!mask||!queue){free(mask);free(queue);return 0;}
 for(int y=0;y<sh;++y)for(int x=0;x<sw;++x){
   const uint8_t *q=rgba+y*step*stride+x*step*4;
   mask[y*sw+x]=q[1]>100&&q[1]-q[0]>55&&q[1]-q[2]>40;
 }
 unsigned count=0;
 for(size_t i=0;i<n;++i){
   if(!mask[i])continue;
   unsigned head=0,tail=0;queue[tail++]=(unsigned)i;mask[i]=0;
   int minx=(int)i%sw,maxx=minx,miny=(int)i/sw,maxy=miny;
   while(head<tail){unsigned at=queue[head++];int x=at%sw,y=at/sw;
     if(x<minx)minx=x;
     if(x>maxx)maxx=x;
     if(y<miny)miny=y;
     if(y>maxy)maxy=y;
     int next[4]={x?((int)at-1):-1,x+1<sw?((int)at+1):-1,y?((int)at-sw):-1,y+1<sh?((int)at+sw):-1};
     for(int k=0;k<4;++k)if(next[k]>=0&&mask[next[k]]){mask[next[k]]=0;queue[tail++]=(unsigned)next[k];}
   }
   float bw=(maxx-minx+1)*step,bh=(maxy-miny+1)*step;
   float x=(minx+maxx)*step*.5f+.5f,y=(miny+maxy)*step*.5f+.5f;
   if(bw<w*.03f||bw>w*.25f||bh<w*.03f||bh>w*.25f||bw/bh<.75f||bw/bh>1.33f||
      tail/(float)((maxx-minx+1)*(maxy-miny+1))<.40f||x<w*.1f||x>w*.9f||y<h*.12f||y>h*.78f)continue;
   /* Refine the coarse component bounds at full resolution. Four-pixel
    * sampling is sufficient for discovery, not for badge center placement. */
   int rx0=(minx-1)*step,ry0=(miny-1)*step,rx1=(maxx+2)*step,ry1=(maxy+2)*step;
   if(rx0<0)rx0=0;
   if(ry0<0)ry0=0;
   if(rx1>w)rx1=w;
   if(ry1>h)ry1=h;
   int fx0=w,fy0=h,fx1=-1,fy1=-1;
   for(int yy=ry0;yy<ry1;++yy)for(int xx=rx0;xx<rx1;++xx){const uint8_t *q=rgba+yy*stride+xx*4;
     if(q[1]>100&&q[1]-q[0]>55&&q[1]-q[2]>40){if(xx<fx0)fx0=xx;if(xx>fx1)fx1=xx;if(yy<fy0)fy0=yy;if(yy>fy1)fy1=yy;}}
   if(fx1>=fx0&&fy1>=fy0){x=(fx0+fx1)*.5f+.5f;y=(fy0+fy1)*.5f+.5f;bw=fx1-fx0+1;bh=fy1-fy0+1;}
   if(count<cap)out[count++]=(struct ez_badge){.src_x=x,.src_y=y,.dst_x=x,.dst_y=y,.radius=fmaxf(bw,bh)*.5f+12};
 }
 free(mask);free(queue);return count;
}
bool ez_badge_world(const double m[6],double u,double v,const struct ez_geometry *g,double out[2]){
 /* Recognition badges use rotated, normalized camera coordinates. The live
  * preview may use a center crop. Derive the quarter-turn from calibration;
  * do not apply a constant offset or stretch the fixed application controls. */
 int ax=fabs(m[0])>fabs(m[1])?0:1,ay=fabs(m[2])>fabs(m[3])?0:1;
 if(ax==ay||g->w<=1||g->h<=1)return false;
 double sx=m[ax]>0?1:-1,sy=m[2+ay]>0?1:-1;
 if(fabs(m[ax])<.5||fabs(m[2+ay])<.5)return false;
 double camera[2];camera[ax]=(u-(sx<0?1:0))/sx;camera[ay]=(v-(sy<0?1:0))/sy;
 out[0]=g->x+g->w*(m[0]*camera[0]+m[1]*camera[1]+m[4]);
 out[1]=g->y+g->h*(m[2]*camera[0]+m[3]*camera[1]+m[5]);
 return isfinite(out[0])&&isfinite(out[1]);
}
bool ez_badge_hit(const struct ez_badge *badges,unsigned count,float x,float y,float *src_x,float *src_y){
 if(count>EZ_MAX_BADGES)return false;
 for(unsigned i=0;i<count;++i){const struct ez_badge *b=badges+i;float dx=x-b->dst_x,dy=y-b->dst_y;
   if(dx*dx+dy*dy<=b->radius*b->radius){*src_x=b->src_x+dx;*src_y=b->src_y+dy;return true;}}
 return false;
}
