/* T8: fork a sleeper and exit. Parent wrapper / pid ns must reap it. */
#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    pid_t p = fork();
    if (p < 0) {
        printf("fork_errno=%d\n", errno);
        return 1;
    }
    if (p == 0) {
        for (;;)
            sleep(30);
    }
    printf("child_pid=%d\n", (int)p);
    return 0;
}
