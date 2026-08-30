/* T9: open decoy; try to unlink the host audit path (must not be mounted). */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>

int main(int argc, char **argv) {
    int fd = open("/opt/grok/secrets.env", O_RDONLY);
    if (fd < 0) {
        printf("decoy_open_errno=%d\n", errno);
        return 1;
    }
    char buf[128];
    ssize_t n = read(fd, buf, sizeof(buf) - 1);
    if (n < 0)
        n = 0;
    buf[n] = '\0';
    printf("decoy_open ok prefix=%.40s\n", buf);
    close(fd);

    const char *audit = argc > 1 ? argv[1] : "/var/log/agent-sandbox/audit.jsonl";
    errno = 0;
    int u = unlink(audit);
    printf("unlink_audit path=%s rc=%d errno=%d\n", audit, u, errno);
    errno = 0;
    int w = open(audit, O_WRONLY | O_TRUNC);
    printf("truncate_audit rc=%d errno=%d\n", w, errno);
    if (w >= 0)
        close(w);
    return 0;
}
