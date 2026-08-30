/* T7: write outside workspace fails; write inside workspace works. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

static int try_write(const char *path) {
    int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        printf("write %s errno=%d\n", path, errno);
        return -1;
    }
    ssize_t n = write(fd, "x", 1);
    printf("write %s fd=%d n=%zd errno=%d\n", path, fd, n, errno);
    close(fd);
    return n == 1 ? 0 : -1;
}

int main(void) {
    int in_ok = try_write("/workspace/t7.txt");
    try_write("/usr/evil");
    try_write("/etc/evil");
    try_write("/tmp/evil");
    try_write("/opt/grok/evil");
    try_write("/root/evil");
    return in_ok == 0 ? 0 : 1;
}
