# honcho_log.py — log-routing helper baked into the honcho-deriver image.
#
# Railway tags every log line by stream: stdout -> severity=info,
# stderr -> severity=error. Python's logging.StreamHandler defaults to
# stderr, so the deriver/reconciler INFO lines surface in Railway as fake
# red errors. The log_config.json baked next to this module routes records
# by level: anything below ERROR goes to stdout, ERROR+ goes to stderr.
# This filter is what lets the stdout handler reject genuine errors so they
# fall through to the stderr handler instead of being duplicated.
import logging


class MaxLevelFilter(logging.Filter):
    """Pass only records at or below ``level``.

    Pairs with a stderr handler pinned to ``level=ERROR`` so each record
    lands on exactly one stream: <ERROR on stdout, >=ERROR on stderr.
    """

    def __init__(self, level=logging.WARNING) -> None:
        super().__init__()
        if isinstance(level, str):
            level = logging.getLevelName(level.upper())
        self.max_level = level if isinstance(level, int) else logging.WARNING

    def filter(self, record: logging.LogRecord) -> bool:
        return record.levelno <= self.max_level
