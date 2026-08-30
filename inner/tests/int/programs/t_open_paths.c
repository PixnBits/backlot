/* Property helper: try to open each argv path; print errno or a prefix. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>

int main(int argc, char **argv) {
    for (int i = 1; i < argc; i++) {
        int fd = open(argv[i], O_RDONLY);
        if (fd < 0) {
            printf("PATH %s errno=%d\n", argv[i], errno);
            continue;
        }
        char buf[80];
        ssize_t n = read(fd, buf, sizeof(buf) - 1);
        if (n < 0)
            n = 0;
        buf[n] = '\0';
        for (ssize_t j = 0; j < n; j++) {
            if (buf[j] < 32 || buf[j] > 126)
                buf[j] = '.';
        }
        printf("PATH %s ok prefix=%s\n", argv[i], buf);
        close(fd);
    }
    return 0;
}
