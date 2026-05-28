# log_filters.py — log-routing helper baked into the gepa image.
#
# Railway tags log lines by stream: stdout -> severity=info, stderr ->
# severity=error. Python's logging.StreamHandler defaults to stderr, so
# uvicorn/app INFO lines surface as fake red errors. log_config.json
# routes records by level (<ERROR -> stdout, ERROR+ -> stderr); this
# filter lets the stdout handler reject genuine errors so they fall
# through to the stderr handler instead of being duplicated.
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
