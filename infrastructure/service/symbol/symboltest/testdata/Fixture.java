package acme.core;

import java.util.List;

/** Service coordinates work. */
public class Service {
    private final List<String> jobs;

    public Service(List<String> jobs) {
        this.jobs = jobs;
    }

    public int size() {
        return jobs.size();
    }

    /** Point is an XY pair. */
    public record Point(int x, int y) {}

    /** Nested helper. */
    static class Helper {
        void aid() {}
    }
}

/** Named marks a thing with a name. */
@interface Named {
    String value();
}

/** Tag is an annotation interface. */
@interface Tag {
    String id() default "";
}
