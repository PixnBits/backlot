/* T10: a binary *named* ls that calls chdir. Name is not policy. */
#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    errno = 0;
    int rc = chdir("/");
    printf("named_ls_chdir rc=%d errno=%d\n", rc, errno);
    return rc == 0 ? 2 : 0;
}
