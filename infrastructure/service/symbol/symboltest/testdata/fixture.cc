#include <cstddef>

namespace net {

// Socket is a TCP endpoint.
class Socket {
public:
    int open() { return fd_; }
private:
    int fd_ = -1;
};

// Addr is an IPv4 tuple.
struct Addr {
    unsigned long ip;
    unsigned short port;
};

}  // namespace net

// bind attaches a socket.
int bind(net::Socket *s, net::Addr a) {
    return s->open();
}

static void internal_fn() {}
