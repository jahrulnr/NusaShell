#include <stdlib.h>

/* Buffer is a growable byte buffer. */
struct Buffer {
    char *data;
    size_t len;
};

/* State is the parser state. */
enum State {
    STATE_INIT,
    STATE_RUN,
    STATE_DONE
};

/* flush writes pending bytes. */
int flush(struct Buffer *b) {
    b->len = 0;
    return 0;
}

static int helper(void) {
    return 1;
}
