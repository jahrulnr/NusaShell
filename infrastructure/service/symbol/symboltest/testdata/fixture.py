"""Engine fixture."""
import asyncio


class Engine:
    """Async job engine."""

    def __init__(self, workers: int = 4):
        self.workers = workers

    @staticmethod
    def default() -> "Engine":
        return Engine()

    async def run(self, job):
        await asyncio.sleep(0)


def boot(config: dict) -> Engine:
    return Engine(config.get("workers", 4))

def _init_cache() -> dict:
    return {}
