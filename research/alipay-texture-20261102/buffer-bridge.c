/* Minimal JNI bridge to public NDK AHardwareBuffer APIs.
 * ABI declarations only; no target application hooks or memory access.
 */
typedef struct AHardwareBuffer AHardwareBuffer;
extern AHardwareBuffer *AHardwareBuffer_fromHardwareBuffer(void *, void *);
extern void *AHardwareBuffer_toHardwareBuffer(void *, AHardwareBuffer *);
extern int AHardwareBuffer_sendHandleToUnixSocket(const AHardwareBuffer *, int);
extern int AHardwareBuffer_recvHandleFromUnixSocket(int, AHardwareBuffer **);
extern void AHardwareBuffer_release(AHardwareBuffer *);
extern int AHardwareBuffer_getId(const AHardwareBuffer *, unsigned long long *);
#define EXPORT __attribute__((visibility("default")))
EXPORT int Java_com_genymobile_scrcpy_BufferBridgeProbe_send(void *env, void *cls, void *buffer, int fd) {
    (void)cls;
    AHardwareBuffer *native = AHardwareBuffer_fromHardwareBuffer(env, buffer);
    return native ? AHardwareBuffer_sendHandleToUnixSocket(native, fd) : -22;
}
EXPORT void *Java_com_genymobile_scrcpy_BufferBridgeProbe_receive(void *env, void *cls, int fd) {
    (void)cls;
    AHardwareBuffer *native = 0;
    if (AHardwareBuffer_recvHandleFromUnixSocket(fd, &native) != 0 || !native) return 0;
    void *result = AHardwareBuffer_toHardwareBuffer(env, native);
    AHardwareBuffer_release(native);
    return result;
}
EXPORT long long Java_com_genymobile_scrcpy_BufferBridgeProbe_id(void *env, void *cls, void *buffer) {
    (void)cls;
    AHardwareBuffer *native = AHardwareBuffer_fromHardwareBuffer(env, buffer);
    unsigned long long result = 0;
    return native && AHardwareBuffer_getId(native, &result) == 0 ? (long long)result : -1;
}
