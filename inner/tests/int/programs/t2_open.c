/* T2: open("/opt/grok/secrets.env") — ENOENT when decoy is off. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    int fd = open("/opt/grok/secrets.env", O_RDONLY);
    if (fd < 0) {
        printf("open_errno=%d\n", errno);
        return 1;
    }
    char buf[64];
    ssize_t n = read(fd, buf, sizeof(buf) - 1);
    if (n < 0)
        n = 0;
    buf[n] = '\0';
    printf("open_ok bytes=%zd prefix=%.24s\n", n, buf);
    close(fd);
    return 0;
}
