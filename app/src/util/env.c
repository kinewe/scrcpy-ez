#include "env.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
# include <windows.h>
# include "util/str.h"
#endif

char *
sc_get_env(const char *varname) {
#ifdef _WIN32
    wchar_t *w_varname = sc_str_to_wchars(varname);
    if (!w_varname) {
         return NULL;
    }
    const wchar_t *value = _wgetenv(w_varname);
    free(w_varname);
    if (!value) {
        return NULL;
    }

    return sc_str_from_wchars(value);
#else
    const char *value = getenv(varname);
    if (!value) {
        return NULL;
    }

    return strdup(value);
#endif
}

bool
sc_set_env(const char *varname, const char *value) {
#ifdef _WIN32
    wchar_t *w_varname = sc_str_to_wchars(varname);
    wchar_t *w_value = sc_str_to_wchars(value);
    if (!w_varname || !w_value) {
        free(w_varname);
        free(w_value);
        return false;
    }
    BOOL ok = SetEnvironmentVariableW(w_varname, w_value);
    free(w_varname);
    free(w_value);
    /* The scrcpy client reads some runtime-switched variables with the C
     * runtime getenv() (e.g. the perspective key colour). SetEnvironmentVariableW
     * updates the Win32 environment block only, so mirror the value into the CRT
     * environment as well or getenv() would still return the old/NULL value. */
    char entry[512];
    int n = snprintf(entry, sizeof(entry), "%s=%s", varname, value);
    if (n > 0 && (size_t) n < sizeof(entry)) {
        _putenv(entry);
    }
    return ok != 0 || n > 0;
#else
    return setenv(varname, value, 1) == 0;
#endif
}
