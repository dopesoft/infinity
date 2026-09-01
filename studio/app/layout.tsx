import type { Metadata, Viewport } from "next";
import localFont from "next/font/local";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import { AuthProvider } from "@/lib/auth/session";
import { NavBadgesProvider } from "@/lib/nav-badges";
import { RealtimeProvider } from "@/lib/realtime/provider";
import { WebSocketProvider } from "@/lib/ws/provider";
import { OnboardingGate } from "@/components/OnboardingGate";
import { TrustToast } from "@/components/TrustToast";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { PWARegister } from "@/components/PWARegister";
import { PullToRefresh } from "@/components/PullToRefresh";
import { RouteLoadingWatcher } from "@/lib/loading";
import "./globals.css";

// Bust edge HTML cache on every request. Without this, Railway/Next caches the
// page HTML for ~1y while emitting new immutable chunk hashes on each deploy,
// so a stale browser sticks to dead chunks (root cause of the "thinking forever"
// bug after a redeploy). HTML must always reflect the latest chunk URLs.
export const dynamic = "force-dynamic";
export const revalidate = 0;

// Majordomo type (docs/studio/MAJORDOMO.md §4). Geist for voice + chrome,
// Geist Mono for data. `geist` is Vercel's own package: it ships the fonts
// locally and wraps them in next/font/local, so there is no Google Fonts
// round trip at build time and no FOUT at runtime.
//
// GeistSans.variable / GeistMono.variable are CLASS NAMES that declare
// --font-geist-sans / --font-geist-mono. Tailwind's `sans`, `voice`, and
// `mono` families read those variables (tailwind.config.ts).
//
// Next 14 needs `transpilePackages: ["geist"]` in next.config.mjs - the
// package ships ESM that the 14.x server bundler will not resolve otherwise.
//
// Instrument Serif is the DISPLAY register (MAJORDOMO §4, amended
// 2026-08-30): the dashboard greeting and the daily quote, and nothing
// else. It is committed under app/fonts rather than pulled through
// next/font/google for the same reason Geist is - no build-time call to
// fonts.googleapis.com and no runtime call to fonts.gstatic.com. See
// app/fonts/README.md for why these are the latin-subset files.
const displaySerif = localFont({
  src: [
    { path: "./fonts/InstrumentSerif-Regular.woff2", weight: "400", style: "normal" },
    { path: "./fonts/InstrumentSerif-Italic.woff2", weight: "400", style: "italic" },
  ],
  variable: "--font-display",
  display: "swap",
  preload: true,
  // Georgia is the metric-nearest serif on both Mac and Windows, so the
  // swap moves the least. It is also what Tailwind's `display` family
  // falls back to (tailwind.config.ts).
  fallback: ["Georgia", "ui-serif", "serif"],
});

const fontVariables = `${GeistSans.variable} ${GeistMono.variable} ${displaySerif.variable}`;

export const metadata: Metadata = {
  title: "Infinity",
  description: "Single-user AI agent with persistent memory.",
  applicationName: "Infinity",
  // The web app manifest unlocks "Add to Home Screen" on iOS Safari (16.4+)
  // and "Install app" on every desktop browser. Apple still wants its own
  // apple-touch-icon link tag, which the `icons.apple` entry below emits.
  manifest: "/manifest.webmanifest",
  icons: {
    icon: [
      { url: "/icon-192.png", sizes: "192x192", type: "image/png" },
      { url: "/icon-512.png", sizes: "512x512", type: "image/png" },
      { url: "/icon.svg", type: "image/svg+xml" },
    ],
    apple: [{ url: "/apple-touch-icon.png", sizes: "180x180", type: "image/png" }],
  },
  appleWebApp: {
    capable: true,
    title: "Infinity",
    statusBarStyle: "default",
  },
  other: {
    "mobile-web-app-capable": "yes",
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 5,
  viewportFit: "cover",
  // resizes-content makes the layout viewport (and 100dvh math) shrink
  // when the iOS keyboard opens, keeping the sticky composer above the
  // keyboard without manual scroll-into-view.
  interactiveWidget: "resizes-content",
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
    { media: "(prefers-color-scheme: dark)", color: "#000000" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className={`${fontVariables} font-sans`}>
        <AuthProvider>
          <RealtimeProvider>
            <NavBadgesProvider>
              <WebSocketProvider>
                <TooltipProvider delayDuration={250}>
                  <PWARegister />
                  {/* Watches every internal link click so the app mark can
                      say "your tap registered" from the one fixed point on
                      the screen. Renders nothing; see lib/loading. */}
                  <RouteLoadingWatcher />
                  <TrustToast />
                  <PullToRefresh>
                    <OnboardingGate>{children}</OnboardingGate>
                  </PullToRefresh>
                  <Toaster />

                </TooltipProvider>
              </WebSocketProvider>
            </NavBadgesProvider>
          </RealtimeProvider>
        </AuthProvider>
      </body>
    </html>
  );
}
