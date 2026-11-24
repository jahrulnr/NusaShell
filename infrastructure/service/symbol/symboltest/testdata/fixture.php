<?php

namespace Acme;

// HasLog adds logging.
trait HasLog {
    public function log(string $m): void {}
}

// Client talks to the API.
class Client {
    use HasLog;

    public function fetch(string $url): array {
        return [];
    }
}

interface Transport {
    public function send(string $payload): bool;
}

function connect(string $host): Client {
    return new Client();
}

class Vault {
    private function secret(): string {
        return "";
    }
}
