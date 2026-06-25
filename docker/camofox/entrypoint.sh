#!/bin/sh
# Railway mounts the persistent profile volume at /home/node/.camofox owned by
# root, but the camofox server runs as the non-root `node` user and must create
# cookies/profiles/downloads dirs under it on boot. Without this the container
# crash-loops on: EACCES mkdir '/home/node/.camofox/cookies'.
#
# Run as root: ensure the dir exists and is owned by `node`, then drop
# privileges and hand off to the image's real CMD (passed as "$@"). The browser
# itself still runs as `node` — Firefox/Camoufox refuse to run as root.
set -e

mkdir -p /home/node/.camofox
chown -R node:node /home/node/.camofox

exec gosu node "$@"
