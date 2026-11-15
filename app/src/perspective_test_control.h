#ifndef SC_PERSPECTIVE_TEST_CONTROL_H
#define SC_PERSPECTIVE_TEST_CONTROL_H
#include <stdio.h>
#include <stdlib.h>
/* Explicit engineering opt-in. No file access or change to shipping behavior
 * unless a caller provides this environment variable. Never accepts commands. */
static inline unsigned sc_perspective_test_mode(void) {
    const char *path=getenv("SCEZ_PERSPECTIVE_TEST_CONTROL");
    if(!path)return 0;
    FILE *f=fopen(path,"r");if(!f)return 0;
    unsigned mode=0;if(fscanf(f,"%u",&mode)!=1||mode>6)mode=0;
    fclose(f);return mode;
}
static inline unsigned sc_perspective_test_bitrate(void) {
    const char *path=getenv("SCEZ_PERSPECTIVE_TEST_CONTROL");if(!path)return 0;
    FILE *f=fopen(path,"r");if(!f)return 0;
    unsigned mode,rate=0;if(fscanf(f,"%u %u",&mode,&rate)!=2||rate<1000000||rate>60000000)rate=0;
    fclose(f);return rate;
}
#endif
