#include <assert.h>
#include "adb/upload_error.h"
int main(void) {
 const char *p="/data/local/tmp/scrcpy-server.jar";
 assert(sc_server_upload_permission_denied("adb: error: failed to copy server to '/data/local/tmp/scrcpy-server.jar': remote couldn't create file: Permission denied",p));
 assert(!sc_server_upload_permission_denied("Permission denied",p));
 assert(!sc_server_upload_permission_denied("failed to stat /data/local/tmp/scrcpy-server.jar: Permission denied",p));
 assert(!sc_server_upload_permission_denied("/data/local/tmp/other: remote couldn't create file: Permission denied",p));
 assert(!sc_server_upload_permission_denied("/data/local/tmp/scrcpy-server.jar: No space left on device",p));
 assert(!sc_server_upload_permission_denied("/data/local/tmp/scrcpy-server.jar: Read-only file system",p));
 assert(!sc_server_upload_permission_denied("/data/local/tmp/scrcpy-server.jar: device offline",p));
 assert(!sc_server_upload_permission_denied("/data/local/tmp/scrcpy-server.jar: remote couldn't create file: Permission denied; Read-only file system",p));
 return 0;
}
