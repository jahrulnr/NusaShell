package acme

// Session is a user session.
data class Session(val id: String, val ttl: Long) {
    fun expired(now: Long): Boolean = now > ttl
}

// Registry keeps sessions.
object Registry {
    private val sessions = mutableMapOf<String, Session>()
}

fun String.shortID(): String = take(8)

fun start(registry: Registry): Session {
    return Session("s1", 60)
}

private fun cleanup() {}
