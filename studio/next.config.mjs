/** @type {import('next').NextConfig} */

// Same-origin proxy to Core.
//
// Studio (infinity.dopesoft.io) and Core (core-production-*.up.railway.app)
// live on different origins. Without this rewrite the browser fires every
// /api/* call cross-origin → CORS preflight → if Core is briefly down
// (deploy swap, cold start, Railway edge blip), the 502 comes back without
// CORS headers and Chrome reports it as a CORS error, not "upstream
// unavailable." That's the wall-of-red the boss kept seeing during deploys.
//
// The fix: rewrite /api/* to Core on the Studio Node server, so the browser
// only ever talks to infinity.dopesoft.io. No preflight, no leaked Railway
// hostname, and an upstream blip is just a generic 502 we can style around
// later.
//
// Destination URL precedence:
//   1. INFINITY_CORE_INTERNAL_URL (preferred — Railway private network,
//      no public-internet hop, no Cloudflare ACL surface).
//   2. CORE_URL fallback (already set on Core; mirrors useful for local).
//   3. Hard-coded public Railway hostname so the build doesn't break if
//      neither var is set.
//
// Browser → infinity.dopesoft.io/api/foo
//        → Studio Node → ${destination}/api/foo
//        → Core
const CORE_INTERNAL =
  process.env.INFINITY_CORE_INTERNAL_URL ||
  process.env.CORE_URL ||
  "http://localhost:8080";

const nextConfig = {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${CORE_INTERNAL}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
