// Inject a real build identifier into the client bundle. Falls back through:
//   1. RAILWAY_GIT_COMMIT_SHA (production deploys)
//   2. VERCEL_GIT_COMMIT_SHA  (mirror — works on Vercel too)
//   3. NEXT_PUBLIC_BUILD      (manual override)
//   4. local timestamp        (dev fallback so the footer never shows "dev")
const sha =
  process.env.RAILWAY_GIT_COMMIT_SHA ||
  process.env.VERCEL_GIT_COMMIT_SHA ||
  process.env.NEXT_PUBLIC_BUILD ||
  "";
const buildId = sha ? sha.slice(0, 7) : new Date().toISOString().slice(0, 10);

/** @type {import('next').NextConfig} */
const nextConfig = {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  env: {
    NEXT_PUBLIC_BUILD: buildId,
  },
};

export default nextConfig;
