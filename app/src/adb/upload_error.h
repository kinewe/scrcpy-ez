#ifndef SC_UPLOAD_ERROR_H
#define SC_UPLOAD_ERROR_H
#include <stdbool.h>
#include <string.h>

// Only a remote create/open/write denial for our actual server upload qualifies.
// A local stat denial, storage failure, or unrelated Permission denied does not.
static inline bool
sc_server_upload_permission_denied(const char *error, const char *destination) {
    return strstr(error, destination)
        && (strstr(error, "remote couldn't create file: Permission denied")
         || strstr(error, "remote couldn't open file: Permission denied")
         || strstr(error, "remote write failed: Permission denied"))
        && !strstr(error, "No space left on device")
        && !strstr(error, "Read-only file system");
}
#endif
