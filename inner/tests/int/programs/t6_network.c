/* T6: direct outbound TCP must fail (network none). */
#define _GNU_SOURCE
#include <arpa/inet.h>
#include <errno.h>
#include <netinet/in.h>
#include <stdio.h>
#include <sys/socket.h>
#include <unistd.h>

int main(void) {
    errno = 0;
    int s = socket(AF_INET, SOCK_STREAM, 0);
    printf("socket fd=%d errno=%d\n", s, errno);
    if (s < 0)
        return 0;
    struct sockaddr_in a;
    a.sin_family = AF_INET;
    a.sin_port = htons(443);
    a.sin_addr.s_addr = inet_addr("1.1.1.1");
    errno = 0;
    int c = connect(s, (struct sockaddr *)&a, sizeof(a));
    printf("connect rc=%d errno=%d\n", c, errno);
    close(s);
    return c == 0 ? 2 : 0;
}
