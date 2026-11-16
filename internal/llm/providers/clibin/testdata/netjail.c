// Scratch-grade test launcher for TERM_LLM_CLI_PROXY_EXEC_JAIL (see cli_proxy_exec_test.go).
// Linux x86-64 only; needs no privileges. Build: gcc -O1 -o netjail testdata/netjail.c
// Set NETJAIL_ALLOW_DNS=1 to let loopback resolver lookups through so refused
// destinations are logged by address, and NETJAIL_ALLOW_UNIX_PREFIX=/dir/ to permit
// unix sockets under a private directory (a CLI's own IPC).
#define _GNU_SOURCE
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <netinet/in.h>
#include <poll.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <unistd.h>

#define HANDOFF_FD 1000
static FILE *logf;

static int readmem(pid_t pid, unsigned long addr, void *buf, size_t n) {
  char path[64];
  snprintf(path, sizeof path, "/proc/%d/mem", pid);
  int fd = open(path, O_RDONLY);
  if (fd < 0) return -1;
  ssize_t r = pread(fd, buf, n, (off_t)addr);
  close(fd);
  return r == (ssize_t)n ? 0 : -1;
}

// returns 1 allow, 0 deny
static int decide(pid_t pid, const char *sys, unsigned long addr, unsigned long len) {
  if (!addr || !len) return 1;
  struct sockaddr_storage ss;
  memset(&ss, 0, sizeof ss);
  if (len > sizeof ss) len = sizeof ss;
  if (readmem(pid, addr, &ss, len) < 0) {
    fprintf(logf, "DENY %s pid=%d unreadable-sockaddr\n", sys, pid);
    return 0;
  }
  char text[INET6_ADDRSTRLEN + 8] = "";
  int allow = 0, port = 0;
  switch (ss.ss_family) {
  case AF_UNSPEC: case AF_NETLINK:
    return 1;
  case AF_INET: {
    struct sockaddr_in *in = (void *)&ss;
    inet_ntop(AF_INET, &in->sin_addr, text, sizeof text);
    port = ntohs(in->sin_port);
    allow = (ntohl(in->sin_addr.s_addr) >> 24) == 127 && (port != 53 || getenv("NETJAIL_ALLOW_DNS"));
    break;
  }
  case AF_INET6: {
    struct sockaddr_in6 *in6 = (void *)&ss;
    inet_ntop(AF_INET6, &in6->sin6_addr, text, sizeof text);
    port = ntohs(in6->sin6_port);
    allow = IN6_IS_ADDR_LOOPBACK(&in6->sin6_addr) ||
            (IN6_IS_ADDR_V4MAPPED(&in6->sin6_addr) && in6->sin6_addr.s6_addr[12] == 127);
    break;
  }
  case AF_UNIX: {
    struct sockaddr_un *un = (void *)&ss;
    snprintf(text, sizeof text, "unix:%.40s", un->sun_path[0] ? un->sun_path : un->sun_path + 1);
    const char *prefix = getenv("NETJAIL_ALLOW_UNIX_PREFIX"); // the CLI's own IPC sockets
    allow = prefix && *prefix && un->sun_path[0] && !strncmp(un->sun_path, prefix, strlen(prefix));
    break;
  }
  default:
    snprintf(text, sizeof text, "family-%d", ss.ss_family);
  }
  fprintf(logf, "%s %s pid=%d %s port=%d\n", allow ? "ALLOW" : "DENY", sys, pid, text, port);
  fflush(logf);
  return allow;
}

int main(int argc, char **argv) {
  if (argc < 4 || strcmp(argv[2], "--")) {
    fprintf(stderr, "usage: netjail LOG -- cmd args...\n");
    return 2;
  }
  logf = fopen(argv[1], "a");
  if (!logf) { perror("log"); return 2; }
  int up[2], down[2];
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, up) || pipe(down)) { perror("pipe"); return 2; }
  pid_t child = fork();
  if (child == 0) {
    close(up[0]); close(down[1]);
    struct sock_filter filter[] = {
      BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, arch)),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, AUDIT_ARCH_X86_64, 1, 0),
      BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS),
      BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_connect, 7, 0),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_sendto, 6, 0),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_sendmmsg, 5, 0),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_sendmsg, 1, 0),
      BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
      BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[0])),
      BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, HANDOFF_FD, 0, 1),
      BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
      BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_USER_NOTIF),
    };
    struct sock_fprog prog = {.len = sizeof filter / sizeof filter[0], .filter = filter};
    if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)) { perror("nnp"); _exit(126); }
    int fd = syscall(SYS_seccomp, SECCOMP_SET_MODE_FILTER, SECCOMP_FILTER_FLAG_NEW_LISTENER, &prog);
    if (fd < 0) { perror("seccomp"); _exit(126); }
    if (dup2(up[1], HANDOFF_FD) < 0) _exit(126);
    char cbuf[CMSG_SPACE(sizeof(int))]; memset(cbuf, 0, sizeof cbuf);
    char one = 'f'; struct iovec iov = {&one, 1};
    struct msghdr m = {.msg_iov = &iov, .msg_iovlen = 1, .msg_control = cbuf, .msg_controllen = sizeof cbuf};
    struct cmsghdr *c = CMSG_FIRSTHDR(&m); c->cmsg_level = SOL_SOCKET; c->cmsg_type = SCM_RIGHTS; c->cmsg_len = CMSG_LEN(sizeof(int));
    memcpy(CMSG_DATA(c), &fd, sizeof(int));
    if (sendmsg(HANDOFF_FD, &m, 0) != 1) { perror("handoff"); _exit(126); }
    close(HANDOFF_FD);
    char go;
    if (read(down[0], &go, 1) != 1) _exit(126);
    close(fd); close(up[1]); close(down[0]);
    execvp(argv[3], argv + 3);
    perror("exec");
    _exit(127);
  }
  close(up[1]); close(down[0]);
  int nfd = -1;
  {
    char cbuf[CMSG_SPACE(sizeof(int))]; char one; struct iovec iov = {&one, 1};
    struct msghdr m = {.msg_iov = &iov, .msg_iovlen = 1, .msg_control = cbuf, .msg_controllen = sizeof cbuf};
    if (recvmsg(up[0], &m, 0) != 1) { fprintf(stderr, "netjail: child setup failed\n"); return 126; }
    struct cmsghdr *c = CMSG_FIRSTHDR(&m);
    if (!c || c->cmsg_type != SCM_RIGHTS) { fprintf(stderr, "netjail: no listener\n"); kill(child, SIGKILL); return 126; }
    memcpy(&nfd, CMSG_DATA(c), sizeof(int));
  }
  if (write(down[1], "g", 1) != 1) return 126;
  struct seccomp_notif_sizes sizes;
  syscall(SYS_seccomp, SECCOMP_GET_NOTIF_SIZES, 0, &sizes);
  struct seccomp_notif *req = calloc(1, sizes.seccomp_notif);
  struct seccomp_notif_resp *resp = calloc(1, sizes.seccomp_notif_resp);
  for (;;) {
    struct pollfd pfd = {.fd = nfd, .events = POLLIN};
    if (poll(&pfd, 1, -1) < 0) { if (errno == EINTR) continue; break; }
    if (pfd.revents & POLLHUP) break;
    memset(req, 0, sizes.seccomp_notif);
    if (ioctl(nfd, SECCOMP_IOCTL_NOTIF_RECV, req) < 0) { if (errno == EINTR || errno == ENOENT) continue; break; }
    int allow = 1;
    if (req->data.nr == SYS_connect) allow = decide(req->pid, "connect", req->data.args[1], req->data.args[2]);
    else if (req->data.nr == SYS_sendto) allow = decide(req->pid, "sendto", req->data.args[4], req->data.args[5]);
    else if (req->data.nr == SYS_sendmsg) {
      struct msghdr mh;
      if (readmem(req->pid, req->data.args[1], &mh, sizeof mh) < 0) allow = 0;
      else allow = decide(req->pid, "sendmsg", (unsigned long)mh.msg_name, mh.msg_namelen);
    } else {
      struct mmsghdr mm;
      if (readmem(req->pid, req->data.args[1], &mm, sizeof mm) < 0) allow = 0;
      else allow = decide(req->pid, "sendmmsg", (unsigned long)mm.msg_hdr.msg_name, mm.msg_hdr.msg_namelen);
    }
    memset(resp, 0, sizes.seccomp_notif_resp);
    resp->id = req->id;
    if (allow) resp->flags = SECCOMP_USER_NOTIF_FLAG_CONTINUE;
    else resp->error = -ENETUNREACH;
    ioctl(nfd, SECCOMP_IOCTL_NOTIF_SEND, resp);
  }
  int status = 0;
  waitpid(child, &status, 0);
  fclose(logf);
  return WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
}
