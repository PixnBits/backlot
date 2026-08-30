/* T3: chdir("/") then chdir("/opt/grok") → EPERM or ENOENT. */
#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    errno = 0;
    int a = chdir("/");
    printf("chdir_slash rc=%d errno=%d\n", a, errno);
    errno = 0;
    int b = chdir("/opt/grok");
    printf("chdir_grok rc=%d errno=%d\n", b, errno);
    return 0;
}
