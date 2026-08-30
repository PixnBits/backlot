/* T5: unshare / clone(CLONE_NEWUSER) must be denied. */
#define _GNU_SOURCE
#include <errno.h>
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <unistd.h>

static int childfn(void *arg) {
    (void)arg;
    return 0;
}

int main(void) {
    errno = 0;
    int u = unshare(CLONE_NEWUSER);
    printf("unshare rc=%d errno=%d\n", u, errno);

    char stack[65536];
    errno = 0;
    int c = clone(childfn, stack + sizeof(stack), CLONE_NEWUSER | SIGCHLD, NULL);
    printf("clone_newuser rc=%d errno=%d\n", c, errno);

    errno = 0;
    long c3 = syscall(SYS_clone3, (void *)0, (unsigned long)0);
    printf("clone3 rc=%ld errno=%d\n", c3, errno);
    return 0;
}
