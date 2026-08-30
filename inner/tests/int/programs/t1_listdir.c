/* T1: list /opt/grok. ENOENT without decoy; decoy names with decoy. */
#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>

int main(void) {
    DIR *d = opendir("/opt/grok");
    if (!d) {
        printf("opendir_errno=%d\n", errno);
        return 1;
    }
    struct dirent *e;
    int n = 0;
    while ((e = readdir(d)) != NULL) {
        if (e->d_name[0] == '.' &&
            (e->d_name[1] == '\0' ||
             (e->d_name[1] == '.' && e->d_name[2] == '\0')))
            continue;
        printf("entry=%s\n", e->d_name);
        n++;
    }
    closedir(d);
    printf("count=%d\n", n);
    return 0;
}
