# Bootstrap for the honcho deriver worker.
#
# The deriver is a worker (no uvicorn), so it can't take uvicorn's
# --log-config. This shim installs honest log routing via dictConfig
# BEFORE src.deriver is imported, so honcho's own logging.basicConfig()
# becomes a no-op and the deriver/reconciler INFO lines route by level
# (<ERROR -> stdout, ERROR+ -> stderr) instead of being mistagged as
# errors by Railway's stream-based severity. Then runs `src.deriver`
# exactly as `python -m src.deriver` would.
import json
import logging.config
import runpy

with open("/app/log_config.json") as fh:
    logging.config.dictConfig(json.load(fh))

runpy.run_module("src.deriver", run_name="__main__", alter_sys=True)
