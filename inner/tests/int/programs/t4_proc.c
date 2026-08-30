/* T4: /proc/1/root and /proc/self/root tricks. Must not leak real secrets. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

static void try_open(const char *path) {
    int fd = open(path, O_RDONLY);
    if (fd < 0) {
        printf("open %s errno=%d\n", path, errno);
        return;
    }
    char buf[96];
    ssize_t n = read(fd, buf, sizeof(buf) - 1);
    if (n < 0)
        n = 0;
    buf[n] = '\0';
    printf("open %s ok prefix=%.80s\n", path, buf);
    close(fd);
}

int main(void) {
    try_open("/proc/1/root/opt/grok/secrets.env");
    try_open("/proc/self/root/opt/grok/secrets.env");
    try_open("/proc/self/cwd/../opt/grok/secrets.env");
    try_open("/root/.ssh/id_rsa");
    try_open("/etc/shadow");
    try_open("/home/user/.ssh/id_rsa");
    return 0;
}
